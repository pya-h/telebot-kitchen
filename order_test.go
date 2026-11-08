package kitchen

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func answers(k *Kitchen) bool {
	resp, err := http.Post(k.APIURL()+"/bot"+k.Token()+"/getMe", "application/json", strings.NewReader("{}"))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type diary struct {
	mu    sync.Mutex
	lines []string
}

func (d *diary) write(line string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append(d.lines, line)
}

func (d *diary) read() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.lines)
}

func TestAKitchensCleanupRunsBeforeItCloses(t *testing.T) {
	var answered bool
	t.Run("the test", func(t *testing.T) {
		k := New(t)
		k.Cleanup(func() { answered = answers(k) })
	})
	if !answered {
		t.Error("a cleanup could not reach the API, want it to run while the kitchen still answers")
	}
}

func TestACleanupRunsOnceAfterTheScriptWhileTheKitchenAnswers(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	var served, cleaned, answered, early atomic.Int64
	Rush{
		Orders:      6,
		Concurrency: 3,
		Bot: func(k *Kitchen) error {
			k.Cleanup(func() {
				cleaned.Add(1)
				if served.Load() == 0 {
					early.Add(1)
				}
				if answers(k) {
					answered.Add(1)
				}
			})
			return silentBot(k)
		},
		Serve: func(*Order) { served.Add(1) },
	}.Run(tb)

	if errs := tb.errors(); len(errs) != 0 {
		t.Fatalf("the rush failed: %v", errs)
	}
	if cleaned.Load() != 6 || answered.Load() != 6 {
		t.Errorf("cleaned %d, answered %d, want each of the 6 orders once, API still up", cleaned.Load(), answered.Load())
	}
	if early.Load() != 0 {
		t.Errorf("%d cleanups ran before their script", early.Load())
	}
}

func TestCleanupsRunLastRegisteredFirst(t *testing.T) {
	rushGate(t, true)

	var order diary
	Rush{
		Orders:      1,
		Concurrency: 1,
		Bot: func(k *Kitchen) error {
			k.Cleanup(func() { order.write("first registered") })
			k.Cleanup(func() { order.write("second registered") })
			return silentBot(k)
		},
		Serve: func(o *Order) { o.Cleanup(func() { order.write("from the script") }) },
	}.Run(&recordingTB{})

	want := []string{"from the script", "second registered", "first registered"}
	if got := order.read(); !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
}

func TestAStuckOrderKeepsItsCleanups(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	release := make(chan struct{})
	defer close(release)

	var cleaned atomic.Int64
	Rush{
		Orders:      1,
		Concurrency: 1,
		Timeout:     60 * time.Millisecond,
		Bot: func(k *Kitchen) error {
			k.Cleanup(func() { cleaned.Add(1) })
			return silentBot(k)
		},
		Serve: func(*Order) { <-release },
	}.Run(tb)

	if cleaned.Load() != 0 {
		t.Error("a stuck order's cleanup ran, want its kitchen left standing")
	}
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "never settled") {
		t.Errorf("reported %v, want the order named stuck", errs)
	}
}

func TestACleanupThatHangsIsReportedRatherThanHangingTheRush(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	release := make(chan struct{})
	defer close(release)

	started := time.Now()
	Rush{
		Orders:      1,
		Concurrency: 1,
		Timeout:     60 * time.Millisecond,
		Bot: func(k *Kitchen) error {
			k.Cleanup(func() { <-release })
			return silentBot(k)
		},
		Serve: func(*Order) {},
	}.Run(tb)

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "a cleanup was still running") {
		t.Errorf("reported %v, want the hanging cleanup named", errs)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("the rush took %s, want a cleanup held to the order's Timeout", took)
	}
}

func TestACleanupThatFailsLeavesTheRestToRun(t *testing.T) {
	cases := map[string]func(*Order){
		"Fatalf": func(o *Order) { o.Fatalf("the drain failed") },
		"panic":  func(*Order) { panic("the drain failed") },
	}
	for name, fail := range cases {
		t.Run(name, func(t *testing.T) {
			rushGate(t, true)
			tb := &recordingTB{}

			var earlier atomic.Int64
			Rush{
				Orders:      1,
				Concurrency: 1,
				Bot:         silentBot,
				Serve: func(o *Order) {
					o.Cleanup(func() { earlier.Add(1) })
					o.Cleanup(func() { fail(o) })
				},
			}.Run(tb)

			if earlier.Load() != 1 {
				t.Error("the cleanup registered earlier never ran")
			}
			if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "the drain failed") {
				t.Errorf("reported %v, want the failing cleanup's words", errs)
			}
		})
	}
}

// A bot's shutdown drains what it still had to send, so that has to be read too.
func TestWhatABotSaysWhileStoppingIsCheckedForForeignTickets(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	Rush{
		Orders:      1,
		Concurrency: 1,
		Bot: func(k *Kitchen) error {
			b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
			if err != nil {
				return err
			}
			k.Cleanup(func() {
				b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: 101, Text: "late: ticket-999"})
			})
			return silentBot(k)
		},
		Serve: func(o *Order) { o.User(101).Send("hello " + o.Ticket) },
	}.Run(tb)

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "ticket-999") {
		t.Errorf("reported %v, want the foreign ticket said during shutdown caught", errs)
	}
}

func TestBuildSeesTheOrderServeSees(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	type seen struct {
		n       int
		ticket  string
		kitchen *Kitchen
	}
	var builds, serves sync.Map
	Rush{
		Orders:      5,
		Concurrency: 2,
		Build: func(o *Order) error {
			builds.Store(o.N, seen{o.N, o.Ticket, o.Kitchen})
			return silentBot(o.Kitchen)
		},
		Serve: func(o *Order) { serves.Store(o.N, seen{o.N, o.Ticket, o.Kitchen}) },
	}.Run(tb)

	if errs := tb.errors(); len(errs) != 0 {
		t.Fatalf("the rush failed: %v", errs)
	}
	for n := 1; n <= 5; n++ {
		built, _ := builds.Load(n)
		served, _ := serves.Load(n)
		if built == nil || built != served {
			t.Errorf("order %d built with %+v, served with %+v", n, built, served)
		}
		if built != nil && built.(seen).ticket != ticketOf(n) {
			t.Errorf("order %d built with ticket %q", n, built.(seen).ticket)
		}
	}
}

func TestARushWantsExactlyOneBuilder(t *testing.T) {
	both := Rush{Bot: silentBot, Build: func(o *Order) error { return silentBot(o.Kitchen) }, Serve: func(*Order) {}}
	neither := Rush{Serve: func(*Order) {}}
	for name, r := range map[string]Rush{"both": both, "neither": neither} {
		t.Run(name, func(t *testing.T) {
			rushGate(t, true)
			tb := &recordingTB{}
			r.Run(tb)
			if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "exactly one of Bot and Build") {
				t.Errorf("reported %v, want the rush refused", errs)
			}
		})
	}
}

func TestAReplayBuildsWithTheSameNumber(t *testing.T) {
	rushGate(t, true)
	replaying(t, 7)

	var built []int
	Rush{
		Orders:      20,
		Concurrency: 5,
		Build: func(o *Order) error {
			built = append(built, o.N)
			return silentBot(o.Kitchen)
		},
		Serve: func(*Order) {},
	}.Run(&recordingTB{})

	if !slices.Equal(built, []int{7}) {
		t.Errorf("built %v, want only order 7", built)
	}
}

func TestWhatBuildDrawsDoesNotMoveTheScript(t *testing.T) {
	rushGate(t, true)

	roll := func(draws int) uint64 {
		var got uint64
		Rush{
			Orders:      1,
			Concurrency: 1,
			Build: func(o *Order) error {
				for range draws {
					o.Rand.Uint64()
				}
				return silentBot(o.Kitchen)
			},
			Serve: func(o *Order) { got = o.Rand.Uint64() },
		}.Run(&recordingTB{})
		return got
	}

	if none, some := roll(0), roll(3); none != some {
		t.Errorf("the script rolled %d after no draws and %d after three, want the same", none, some)
	}
}

func TestABuilderCanFailItsOrder(t *testing.T) {
	cases := map[string]struct {
		build func(*Order) error
		want  string
	}{
		"error":  {func(*Order) error { return fmt.Errorf("no redis") }, "build the bot: no redis"},
		"Errorf": {func(o *Order) error { o.Errorf("no redis"); return nil }, "no redis"},
		"Fatalf": {func(o *Order) error { o.Fatalf("no redis"); return nil }, "no redis"},
		"panic":  {func(*Order) error { panic("no redis") }, "panicked: no redis"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rushGate(t, true)
			tb := &recordingTB{}

			var served, cleaned atomic.Int64
			Rush{
				Orders:      1,
				Concurrency: 1,
				Build: func(o *Order) error {
					o.Cleanup(func() { cleaned.Add(1) })
					return c.build(o)
				},
				Serve: func(*Order) { served.Add(1) },
			}.Run(tb)

			if served.Load() != 0 {
				t.Error("an order that could not build was served anyway")
			}
			if cleaned.Load() != 1 {
				t.Errorf("cleaned %d times, want what was built taken down once", cleaned.Load())
			}
			if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], c.want) {
				t.Errorf("reported %v, want %q", errs, c.want)
			}
		})
	}
}

func TestErrorfBreaksAnOrderWithItsWords(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	var failed atomic.Bool
	Rush{
		Orders:      1,
		Concurrency: 1,
		Bot:         silentBot,
		Serve: func(o *Order) {
			o.Helper()
			o.Errorf("%d grants, want 1", 2)
			failed.Store(o.Failed())
		},
	}.Run(tb)

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "2 grants, want 1") {
		t.Errorf("reported %v, want the order's own words", errs)
	}
	if !failed.Load() {
		t.Error("Failed said false after Errorf")
	}
}

func TestFatalfStopsTheScriptAndIsNotAPanicOrAHang(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	var after atomic.Bool
	Rush{
		Orders:      1,
		Concurrency: 1,
		Timeout:     5 * time.Second,
		Bot:         silentBot,
		Serve: func(o *Order) {
			o.Fatalf("no row for chat %d", 7)
			after.Store(true)
		},
	}.Run(tb)

	if after.Load() {
		t.Error("the script went on after Fatalf")
	}
	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "no row for chat 7") {
		t.Fatalf("reported %v, want Fatalf's words", errs)
	}
	for _, wrong := range []string{"panicked", "never settled"} {
		if strings.Contains(errs[0], wrong) {
			t.Errorf("reported %q, want no mention of %q", errs[0], wrong)
		}
	}
}

func TestAnOrdersLogShowsOnlyWhenItBreaks(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	Rush{
		Orders:      2,
		Concurrency: 1,
		Bot:         silentBot,
		Serve: func(o *Order) {
			o.Logf("order %d was here", o.N)
			if o.N == 2 {
				o.Errorf("broke")
			}
		},
	}.Run(tb)

	errs := tb.errors()
	if len(errs) != 1 {
		t.Fatalf("reported %v, want one report", errs)
	}
	if !strings.Contains(errs[0], "order 2 was here") {
		t.Errorf("report %q, want the broken order's log", errs[0])
	}
	if strings.Contains(errs[0], "order 1 was here") {
		t.Errorf("report %q, want a healthy order's log dropped", errs[0])
	}
}

func TestAnOrdersLogKeepsItsLastLines(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	Rush{
		Orders:      1,
		Concurrency: 1,
		Bot:         silentBot,
		Serve: func(o *Order) {
			for i := 1; i <= mostLogLines+5; i++ {
				o.Logf("line <%d>", i)
			}
			o.Errorf("broke")
		},
	}.Run(tb)

	errs := tb.errors()
	if len(errs) != 1 {
		t.Fatalf("reported %v, want one report", errs)
	}
	for _, gone := range []string{"line <1>", "line <5>"} {
		if strings.Contains(errs[0], gone) {
			t.Errorf("the report still holds %q, want only the last %d lines", gone, mostLogLines)
		}
	}
	for _, kept := range []string{"line <6>", fmt.Sprintf("line <%d>", mostLogLines+5), "5 earlier lines dropped"} {
		if !strings.Contains(errs[0], kept) {
			t.Errorf("the report lacks %q", kept)
		}
	}
}

func TestWhatTheKitchenNotesGoesInTheLogToo(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	Rush{
		Orders:      1,
		Concurrency: 1,
		Options:     []Option{WithAllowedUpdates("message")},
		Bot:         silentBot,
		Serve: func(o *Order) {
			ada := o.User(101)
			o.deliver(models.Update{EditedMessage: &models.Message{Chat: models.Chat{ID: ada.ChatID()}}})
			o.Errorf("broke")
		},
	}.Run(tb)

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "dropped edited_message") {
		t.Errorf("reported %v, want the kitchen's own note beside the failure", errs)
	}
}
