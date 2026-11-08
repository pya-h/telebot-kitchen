package load

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	kitchen "github.com/pya-h/telebot-kitchen"
)

func answers(k *kitchen.Kitchen) bool {
	resp, err := http.Post(k.APIURL()+"/bot"+k.Token()+"/getMe", "application/json", strings.NewReader("{}"))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func echoBot(k *kitchen.Kitchen) error {
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
	if err != nil {
		return err
	}
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.Message == nil {
			return
		}
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: u.Message.Chat.ID,
			Text:   "echo: " + u.Message.Text,
		})
	})
	return nil
}

func silentBot(k *kitchen.Kitchen) error {
	k.DeliverTo(func(context.Context, *models.Update) {})
	return nil
}

func TestARunMeasuresEveryOrder(t *testing.T) {
	report := Run{
		Orders:      12,
		Concurrency: 4,
		Bot:         echoBot,
		Serve: func(o *Order) {
			ada := o.User(101)
			ada.Send(o.Ticket)
			ada.Expect(kitchen.TextIs("echo: " + o.Ticket))
		},
	}.Measure()

	if report.Order.Count != 12 {
		t.Errorf("timed %d conversations, want 12", report.Order.Count)
	}
	if report.Rate <= 0 || report.Elapsed <= 0 {
		t.Errorf("report = %+v, want real time to have passed", report)
	}
	if len(report.Failures) != 0 {
		t.Errorf("failures = %v, want a sound bot to come back clean", report.Failures)
	}
	if report.Goroutines <= 0 {
		t.Errorf("peak goroutines = %d, want the sampler to have seen the run", report.Goroutines)
	}
}

func TestStepsAreReportedByName(t *testing.T) {
	report := Run{
		Orders:      6,
		Concurrency: 2,
		Bot:         echoBot,
		Serve: func(o *Order) {
			ada := o.User(101)
			o.Step("say hello", func() {
				ada.Send(o.Ticket)
				ada.Expect(kitchen.TextContains(o.Ticket))
			})
			o.Step("again", func() {
				ada.Send(o.Ticket)
				ada.Expect(kitchen.TextContains(o.Ticket))
			})
		},
	}.Measure()

	if len(report.Steps) != 2 {
		t.Fatalf("steps = %+v, want the two the script named", report.Steps)
	}
	// Sorted by name, so the same script reports the same way twice.
	if report.Steps[0].Name != "again" || report.Steps[1].Name != "say hello" {
		t.Errorf("steps = %q, %q, want them in name order", report.Steps[0].Name, report.Steps[1].Name)
	}
	for _, s := range report.Steps {
		if s.Count != 6 {
			t.Errorf("step %q counted %d, want one per order", s.Name, s.Count)
		}
		if s.P50 <= 0 || s.P95 < s.P50 || s.Max < s.P95 {
			t.Errorf("step %q spread = %+v, want p50 <= p95 <= max", s.Name, s)
		}
	}
}

// A load run counts what broke; naming each one is what a rush is for.
func TestFailuresAreCountedByKind(t *testing.T) {
	cases := map[string]struct {
		run  Run
		kind string
	}{
		"assertion": {kind: "assertion", run: Run{
			Bot: silentBot,
			Serve: func(o *Order) {
				ada := o.User(101)
				ada.Send(o.Ticket)
				ada.Expect(kitchen.TextContains(o.Ticket))
			},
		}},
		"build": {kind: "build", run: Run{
			Bot:   func(*kitchen.Kitchen) error { return context.Canceled },
			Serve: func(*Order) {},
		}},
		"stuck": {kind: "stuck", run: Run{
			Timeout: 60 * time.Millisecond,
			Bot:     silentBot,
			Serve:   func(*Order) { time.Sleep(2 * time.Second) },
		}},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			run := c.run
			run.Orders, run.Concurrency = 2, 2
			run.Options = append(run.Options, kitchen.WithWaitTimeout(80*time.Millisecond))

			report := run.Measure()
			if report.Failures[c.kind] != 2 {
				t.Errorf("failures = %v, want both orders counted as %q", report.Failures, c.kind)
			}
		})
	}
}

// Without a floor to read them against, every number silently carries the
// harness's own cost.
func TestTheKitchensOwnCostIsMeasuredSeparately(t *testing.T) {
	report := Run{
		Orders:      8,
		Concurrency: 4,
		Bot:         echoBot,
		Serve: func(o *Order) {
			ada := o.User(101)
			ada.Send(o.Ticket)
			ada.Expect(kitchen.TextContains(o.Ticket))
		},
	}.Measure()

	if report.Baseline.Count != 8 {
		t.Errorf("baseline timed %d, want one empty order per real one", report.Baseline.Count)
	}
	if report.Baseline.P50 <= 0 {
		t.Errorf("baseline = %+v, want the kitchen's own cost measured", report.Baseline)
	}
	if report.Net() != report.Order.P50-report.Baseline.P50 {
		t.Errorf("net = %s, want the conversation less the kitchen", report.Net())
	}
}

func TestAnIncompleteRunSaysSoRatherThanMeasuringNothing(t *testing.T) {
	report := Run{Orders: 3, Bot: echoBot}.Measure()
	if report.Failures["setup"] != 3 {
		t.Errorf("failures = %v, want a run with no conversation to say so", report.Failures)
	}
}

func TestBuildSeesTheOrderServeSees(t *testing.T) {
	var mu sync.Mutex
	built := map[int]string{}
	served := map[int]string{}

	report := Run{
		Orders:      6,
		Concurrency: 3,
		Build: func(o *Order) error {
			mu.Lock()
			defer mu.Unlock()
			built[o.N] = o.Ticket
			return silentBot(o.Kitchen)
		},
		Serve: func(o *Order) {
			mu.Lock()
			defer mu.Unlock()
			served[o.N] = o.Ticket
		},
	}.Measure()

	if len(report.Failures) != 0 {
		t.Fatalf("failures = %v", report.Failures)
	}
	if len(built) != 6 || !maps.Equal(built, served) {
		t.Errorf("built %v, served %v, want the same six orders", built, served)
	}
}

func TestARunWantsExactlyOneBuilder(t *testing.T) {
	build := func(o *Order) error { return silentBot(o.Kitchen) }
	serve := func(*Order) {}
	for name, run := range map[string]Run{
		"both":    {Orders: 2, Bot: silentBot, Build: build, Serve: serve},
		"neither": {Orders: 2, Serve: serve},
	} {
		t.Run(name, func(t *testing.T) {
			report := run.Measure()
			if report.Failures["setup"] != 2 {
				t.Errorf("failures = %v, want the run refused", report.Failures)
			}
			if !strings.Contains(report.Samples["setup"], "exactly one of Bot and Build") {
				t.Errorf("samples = %v, want the reason given", report.Samples)
			}
		})
	}
}

func TestASlowBuildIsNotPartOfTheConversation(t *testing.T) {
	const slow = 120 * time.Millisecond
	report := Run{
		Orders:      2,
		Concurrency: 2,
		Build: func(o *Order) error {
			time.Sleep(slow)
			return silentBot(o.Kitchen)
		},
		Serve: func(*Order) {},
	}.Measure()

	if report.Build.Count != 2 || report.Build.P50 < slow {
		t.Errorf("build = %+v, want both builds timed at %s or more", report.Build, slow)
	}
	if report.Order.Max >= slow {
		t.Errorf("conversation = %+v, want a build of %s kept out of it", report.Order, slow)
	}
	if !strings.Contains(report.String(), "build ") {
		t.Errorf("report leaves the build out:\n%s", report)
	}
}

func TestABotBuilderStillCountsInTheConversation(t *testing.T) {
	const slow = 80 * time.Millisecond
	report := Run{
		Orders: 1,
		Bot: func(k *kitchen.Kitchen) error {
			time.Sleep(slow)
			return silentBot(k)
		},
		Serve: func(*Order) {},
	}.Measure()

	if report.Order.Max < slow {
		t.Errorf("conversation = %+v, want a Bot's time left where it has always been", report.Order)
	}
	if report.Build.Count != 0 {
		t.Errorf("build = %+v, want it only for a Build", report.Build)
	}
}

func TestACleanupRunsOnceWhileTheKitchenAnswers(t *testing.T) {
	var served, cleaned, answered atomic.Int64
	report := Run{
		Orders:      4,
		Concurrency: 2,
		Build: func(o *Order) error {
			o.Cleanup(func() {
				cleaned.Add(1)
				if served.Load() > 0 && answers(o.Kitchen) {
					answered.Add(1)
				}
			})
			return silentBot(o.Kitchen)
		},
		Serve: func(*Order) { served.Add(1) },
	}.Measure()

	if len(report.Failures) != 0 {
		t.Fatalf("failures = %v", report.Failures)
	}
	if cleaned.Load() != 4 || answered.Load() != 4 {
		t.Errorf("cleaned %d, answered %d, want every order once, API still up", cleaned.Load(), answered.Load())
	}
}

func TestCleanupsRunLastRegisteredFirst(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	note := func(what string) func() {
		return func() {
			mu.Lock()
			defer mu.Unlock()
			ran = append(ran, what)
		}
	}

	Run{
		Orders: 1,
		Bot: func(k *kitchen.Kitchen) error {
			k.Cleanup(note("first"))
			k.Cleanup(note("second"))
			return silentBot(k)
		},
		Serve: func(*Order) {},
	}.Measure()

	if want := []string{"second", "first"}; !slices.Equal(ran, want) {
		t.Errorf("ran %v, want %v", ran, want)
	}
}

func TestAStuckOrderKeepsItsCleanups(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	var cleaned atomic.Int64
	report := Run{
		Orders:  1,
		Timeout: 60 * time.Millisecond,
		Bot: func(k *kitchen.Kitchen) error {
			k.Cleanup(func() { cleaned.Add(1) })
			return silentBot(k)
		},
		Serve: func(*Order) { <-release },
	}.Measure()

	if report.Failures["stuck"] != 1 {
		t.Errorf("failures = %v, want it counted stuck", report.Failures)
	}
	if cleaned.Load() != 0 {
		t.Error("a stuck order's cleanup ran, want its kitchen left standing")
	}
}

func TestACleanupThatHangsCountsAsStuck(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	report := Run{
		Orders:  1,
		Timeout: 60 * time.Millisecond,
		Bot: func(k *kitchen.Kitchen) error {
			k.Cleanup(func() { <-release })
			return silentBot(k)
		},
		Serve: func(*Order) {},
	}.Measure()

	if report.Failures["stuck"] != 1 || !strings.Contains(report.Samples["stuck"], "a cleanup was still running") {
		t.Errorf("failures = %v, samples = %v", report.Failures, report.Samples)
	}
}

func TestFatalfStopsTheScript(t *testing.T) {
	var after atomic.Bool
	report := Run{
		Orders: 1,
		Bot:    silentBot,
		Serve: func(o *Order) {
			o.Helper()
			o.Logf("nowhere to go")
			o.Fatalf("2 grants")
			after.Store(true)
		},
	}.Measure()

	if after.Load() {
		t.Error("the script went on after Fatalf")
	}
	if report.Failures["assertion"] != 1 {
		t.Errorf("failures = %v", report.Failures)
	}
}

func TestErrorfIsSeenByFailed(t *testing.T) {
	var failed atomic.Bool
	Run{
		Orders: 1,
		Bot:    silentBot,
		Serve: func(o *Order) {
			o.Errorf("2 grants")
			failed.Store(o.Failed())
		},
	}.Measure()

	if !failed.Load() {
		t.Error("Failed said false after Errorf")
	}
}

func TestAReportCarriesOneSamplePerKindAndItsSeed(t *testing.T) {
	report := Run{
		Orders:      6,
		Concurrency: 1,
		Seed:        41,
		Timeout:     60 * time.Millisecond,
		Bot:         silentBot,
		Serve: func(o *Order) {
			switch o.N {
			case 2, 3:
				o.Errorf("wrong grant in order %d", o.N)
			case 5:
				time.Sleep(time.Second)
			}
		},
	}.Measure()

	if report.Seed != 41 {
		t.Errorf("seed = %d, want 41", report.Seed)
	}
	if want := "order 2: wrong grant in order 2"; report.Samples["assertion"] != want {
		t.Errorf("assertion sample = %q, want %q", report.Samples["assertion"], want)
	}
	if !strings.Contains(report.Samples["stuck"], "order 5: the order was still working") {
		t.Errorf("stuck sample = %q", report.Samples["stuck"])
	}

	text := report.String()
	for _, want := range []string{"seed 41", "assertion: 2 orders", "order 2: wrong grant in order 2", "stuck: 1 orders"} {
		if !strings.Contains(text, want) {
			t.Errorf("report is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "wrong grant in order 3") {
		t.Errorf("report has two samples of one kind:\n%s", text)
	}
}

func TestABuildFailureKeepsItsMessage(t *testing.T) {
	cases := map[string]func(*Order) error{
		"error":  func(*Order) error { return context.Canceled },
		"Fatalf": func(o *Order) error { o.Fatalf("context canceled"); return nil },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			var served atomic.Int64
			report := Run{Orders: 1, Build: build, Serve: func(*Order) { served.Add(1) }}.Measure()

			if report.Failures["build"] != 1 || !strings.Contains(report.Samples["build"], "context canceled") {
				t.Errorf("failures = %v, samples = %v", report.Failures, report.Samples)
			}
			if served.Load() != 0 {
				t.Error("an order that could not build was served anyway")
			}
		})
	}
}

func TestPercentileTakesTheNearestRank(t *testing.T) {
	sorted := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := map[float64]time.Duration{0: 1, 0.5: 5, 0.95: 10, 1: 10}
	for q, want := range cases {
		if got := percentile(sorted, q); got != want {
			t.Errorf("percentile(%.2f) = %v, want %v", q, got, want)
		}
	}
}

func TestTheReportReads(t *testing.T) {
	report := Run{
		Orders:      4,
		Concurrency: 2,
		Bot:         echoBot,
		Serve: func(o *Order) {
			ada := o.User(101)
			o.Step("echo", func() {
				ada.Send(o.Ticket)
				ada.Expect(kitchen.TextContains(o.Ticket))
			})
		},
	}.Measure()

	text := report.String()
	for _, want := range []string{
		"4 orders at 2-way concurrency",
		"orders/sec",
		"conversation",
		"kitchen alone",
		"net of the kitchen",
		"echo",
		"peak goroutines",
		"every order came back clean",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report is missing %q:\n%s", want, text)
		}
	}
}
