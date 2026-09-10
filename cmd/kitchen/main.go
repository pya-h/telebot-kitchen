// Command kitchen serves the fake Bot API over a real port, so a bot in any
// language can be pointed at it and driven by hand.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pya-h/telebot-kitchen"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "the address to serve the Bot API on")
	token := flag.String("token", "", "the bot token to answer for; the kitchen's own by default")
	name := flag.String("name", "Kitchen", "the bot's display name")
	username := flag.String("username", "kitchen_bot", "the bot's @username")
	flag.Parse()

	said := kitchen.Logging(os.Stderr)
	opts := []kitchen.Option{
		kitchen.WithAddress(*addr),
		kitchen.WithBotName(*name),
		kitchen.WithBotUsername(*username),
	}
	if *token != "" {
		opts = append(opts, kitchen.WithToken(*token))
	}

	k := kitchen.New(said, opts...)
	defer said.Close()
	if said.Failed() {
		os.Exit(1)
	}
	k.DeliverOverHTTP()

	fmt.Printf("kitchen serving on %s\n", k.APIURL())
	fmt.Printf("  token   %s\n", k.Token())
	fmt.Printf("  bot api %s/bot%s/<method>\n", k.APIURL(), k.Token())
	fmt.Printf("  control %s/kitchen/<verb>\n", k.APIURL())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("kitchen closing")
}
