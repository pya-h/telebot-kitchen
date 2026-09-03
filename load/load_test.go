package load

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	kitchen "github.com/pya-h/telebot-kitchen"
)

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
