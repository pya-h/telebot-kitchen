package kitchen

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// The harness's own tests run the gate themselves, so they keep working whether
// or not the run that carries them asked for rushes.
func rushGate(t *testing.T, on bool) {
	t.Helper()
	prev := *runRushes
	*runRushes = on
	t.Cleanup(func() { *runRushes = prev })
}

func replaying(t *testing.T, order int) {
	t.Helper()
	prev := *rushOrder
	*rushOrder = order
	t.Cleanup(func() { *rushOrder = prev })
}

// A rush needs a bot per kitchen. These are the smallest ones that can be wrong
// in the ways a rush looks for.
func echoBot(k *Kitchen) error {
	return answering(k, func(text string) string { return "echo: " + text })
}

func silentBot(k *Kitchen) error {
	k.DeliverTo(func(context.Context, *models.Update) {})
	return nil
}

// Answers every conversation with the same words, whoever asked: two orders
// running at once then see each other's.
func confusedBot(text string) func(*Kitchen) error {
	return func(k *Kitchen) error { return answering(k, func(string) string { return text }) }
}

func answering(k *Kitchen, reply func(string) string) error {
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
	if err != nil {
		return err
	}
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.Message == nil {
			return
		}
		b.SendMessage(ctx, &bot.SendMessageParams{ChatID: u.Message.Chat.ID, Text: reply(u.Message.Text)})
	})
	return nil
}

func TestARushSkipsUnlessAskedFor(t *testing.T) {
	rushGate(t, false)

	var served atomic.Int64
	Rush{
		Orders: 5,
		Bot:    echoBot,
		Serve:  func(*Order) { served.Add(1) },
	}.Run(&recordingTB{})

	if got := served.Load(); got != 0 {
		t.Errorf("served %d orders, want none until -kitchen.stress asks for them", got)
	}
}

func TestARushRunsEveryOrderOnItsOwnTicket(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	var served atomic.Int64
	var tickets sync.Map
	Rush{
		Orders:      20,
		Concurrency: 5,
		Bot:         echoBot,
		Serve: func(o *Order) {
			served.Add(1)
			tickets.Store(o.Ticket, o.N)

			ada := o.User(101)
			ada.Send("hello " + o.Ticket)
			ada.Expect(TextContains(o.Ticket))
			ada.ExpectNothingMore()
		},
	}.Run(tb)

	if got := served.Load(); got != 20 {
		t.Errorf("served %d orders, want 20", got)
	}
	distinct := 0
	tickets.Range(func(any, any) bool { distinct++; return true })
	if distinct != 20 {
		t.Errorf("%d distinct tickets, want one per order", distinct)
	}
	if errs := tb.errors(); len(errs) != 0 {
		t.Errorf("a sound bot broke the rush:\n%s", strings.Join(errs, "\n"))
	}
}

// The failure no race detector can see: the bot answers one conversation into
// another, and every order but the one that ticket belongs to notices.
func TestARushCatchesAReplyFromAnotherConversation(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	Rush{
		Orders:      3,
		Concurrency: 3,
		Bot:         confusedBot(ticketOf(1)),
		Serve: func(o *Order) {
			o.User(101).Send("hello " + o.Ticket)
			o.Settle()
		},
	}.Run(tb)

	errs := tb.errors()
	if len(errs) != 1 {
		t.Fatalf("reported %d times, want one report for the whole rush:\n%s", len(errs), strings.Join(errs, "\n"))
	}
	report := errs[0]
	if !strings.Contains(report, "2 of 3 orders broke") {
		t.Errorf("report = %q, want the two orders that saw the first one's ticket", report)
	}
	if !strings.Contains(report, "belongs to another conversation") {
		t.Errorf("report = %q, want the cross-chat reply named", report)
	}
	if strings.Contains(report, "order 1:") {
		t.Errorf("report = %q, want the order the ticket belongs to left alone", report)
	}
}

func TestARushCatchesALostReply(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	Rush{
		Orders:      2,
		Concurrency: 2,
		Options:     []Option{WithWaitTimeout(150 * time.Millisecond)},
		Bot:         silentBot,
		Serve: func(o *Order) {
			ada := o.User(101)
			ada.Send("hello " + o.Ticket)
			ada.Expect(TextContains(o.Ticket))
		},
	}.Run(tb)

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "a reply to user 101") {
		t.Errorf("reported %v, want both orders reported as never answered", errs)
	}
	if !strings.Contains(errs[0], "2 of 2 orders broke") {
		t.Errorf("report = %q, want both orders counted", errs[0])
	}
}

func TestARushCatchesAStuckOrder(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	release := make(chan struct{})
	defer close(release)

	Rush{
		Orders:      1,
		Concurrency: 1,
		Timeout:     80 * time.Millisecond,
		Bot:         silentBot,
		Serve:       func(*Order) { <-release },
	}.Run(tb)

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "never settled") {
		t.Errorf("reported %v, want the stuck order named rather than a hung run", errs)
	}
}

func TestARushSaysHowToReplayABrokenOrder(t *testing.T) {
	rushGate(t, true)
	tb := &recordingTB{}

	Rush{
		Orders:      2,
		Concurrency: 1,
		Seed:        77,
		Bot:         confusedBot(ticketOf(1)),
		Serve: func(o *Order) {
			o.User(101).Send("hello " + o.Ticket)
			o.Settle()
		},
	}.Run(tb)

	want := "-kitchen.stress -kitchen.seed=77 -kitchen.order=2"
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], want) {
		t.Errorf("report = %v, want the command that replays the first broken order", errs)
	}
}

func TestReplayingRunsThatOrderAlone(t *testing.T) {
	rushGate(t, true)
	replaying(t, 7)

	var served []string
	var mu sync.Mutex
	Rush{
		Orders:      20,
		Concurrency: 5,
		Bot:         echoBot,
		Serve: func(o *Order) {
			mu.Lock()
			defer mu.Unlock()
			served = append(served, o.Ticket)
		},
	}.Run(&recordingTB{})

	if len(served) != 1 || served[0] != ticketOf(7) {
		t.Errorf("served %v, want only the order being replayed", served)
	}
}

// The seed reaches the script, so a rush that varies itself replays the same way.
func TestAnOrderRandomizesFromTheSeed(t *testing.T) {
	rushGate(t, true)

	roll := func(seed int64) []uint64 {
		var mu sync.Mutex
		var got []uint64
		Rush{
			Orders:      4,
			Concurrency: 1,
			Seed:        seed,
			Bot:         silentBot,
			Serve: func(o *Order) {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, o.Rand.Uint64())
			},
		}.Run(&recordingTB{})
		return got
	}

	first, again, other := roll(5), roll(5), roll(6)
	if len(first) != 4 {
		t.Fatalf("rolled %v, want one per order", first)
	}
	if !slices.Equal(first, again) {
		t.Errorf("the same seed rolled %v then %v, want a replayable rush", first, again)
	}
	if slices.Equal(first, other) {
		t.Errorf("seeds 5 and 6 both rolled %v, want the seed to matter", first)
	}
}
