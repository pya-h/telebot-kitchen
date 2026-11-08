package example

import (
	"testing"

	kitchen "github.com/pya-h/telebot-kitchen"
)

// An ordinary run skips this; go test -kitchen.stress asks for it. The bot keeps
// each chat's settings in a map behind a mutex, so the question worth asking
// under load is whether one conversation can ever be answered into another.
func TestTheMenuHoldsUpUnderLoad(t *testing.T) {
	kitchen.Rush{
		Orders:      120,
		Concurrency: 12,
		Bot: func(k *kitchen.Kitchen) error {
			b, err := New(k.APIURL(), k.Token())
			if err != nil {
				return err
			}
			k.DeliverTo(b.ProcessUpdate)
			return nil
		},
		Serve: func(o *kitchen.Order) {
			ada := o.User(101, kitchen.WithFullName("Ada", "Lovelace"))

			// The echo carries the ticket, which is what the cross-chat check reads.
			ada.Send(o.Ticket)
			ada.Expect(kitchen.TextIs("echo: " + o.Ticket))

			ada.SendCommand("start")
			ada.Expect(kitchen.HasButton("English"))

			ada.Tap("English")
			ada.ExpectScreen(kitchen.HasButton("Done"))

			ada.Tap("Done")
			ada.ExpectScreen(kitchen.TextContains("All set"))
			ada.ExpectNothingMore()
		},
	}.Run(t)
}

// A bot built per order is told which order it is for, and is stopped with it.
func TestEveryOrderBuildsAndStopsItsOwnBot(t *testing.T) {
	kitchen.Rush{
		Orders:      40,
		Concurrency: 8,
		Build: func(o *kitchen.Order) error {
			b, err := New(o.APIURL(), o.Token())
			if err != nil {
				return err
			}
			o.DeliverTo(b.ProcessUpdate)
			o.Cleanup(func() {
				// Runs once the script has returned, with the kitchen still answering.
				o.Logf("order %d stopped its bot", o.N)
			})
			return nil
		},
		Serve: func(o *kitchen.Order) {
			ada := o.User(int64(1000 + o.N))
			ada.Send(o.Ticket)
			ada.Expect(kitchen.TextIs("echo: " + o.Ticket))

			if sent := o.Calls().Count(kitchen.Method("sendMessage")); sent != 1 {
				o.Fatalf("the bot sent %d messages, want 1", sent)
			}
		},
	}.Run(t)
}
