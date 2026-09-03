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
