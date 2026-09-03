package main

import (
	"flag"
	"fmt"
	"os"

	kitchen "github.com/pya-h/telebot-kitchen"
	"github.com/pya-h/telebot-kitchen/example"
	"github.com/pya-h/telebot-kitchen/load"
)

func main() {
	orders := flag.Int("orders", 200, "conversations to run")
	concurrency := flag.Int("concurrency", 16, "how many to run at once")
	flag.Parse()

	report := load.Run{
		Orders:      *orders,
		Concurrency: *concurrency,
		Bot: func(k *kitchen.Kitchen) error {
			b, err := example.New(k.APIURL(), k.Token())
			if err != nil {
				return err
			}
			k.DeliverTo(b.ProcessUpdate)
			return nil
		},
		Serve: func(o *load.Order) {
			ada := o.User(101, kitchen.WithFullName("Ada", "Lovelace"))

			o.Step("echo", func() {
				ada.Send(o.Ticket)
				ada.Expect(kitchen.TextIs("echo: " + o.Ticket))
			})
			o.Step("open the menu", func() {
				ada.SendCommand("start")
				ada.Expect(kitchen.HasButton("English"))
			})
			o.Step("pick a language", func() {
				ada.Tap("English")
				ada.ExpectScreen(kitchen.HasButton("Done"))
			})
			o.Step("finish", func() {
				ada.Tap("Done")
				ada.ExpectScreen(kitchen.TextContains("All set"))
			})
		},
	}.Measure()

	fmt.Print(report)
	if len(report.Failures) > 0 {
		os.Exit(1)
	}
}
