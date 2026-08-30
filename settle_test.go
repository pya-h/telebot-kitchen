package kitchen

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// asyncBot handles updates the way the library does by default: on its own
// goroutine, so nothing has happened yet when the user's verb returns.
func asyncBot(t *testing.T, k *Kitchen, handler bot.HandlerFunc) *bot.Bot {
	t.Helper()
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()), bot.WithDefaultHandler(handler))
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}
	return b
}

func TestExpectReplyWaitsForAnAsyncHandler(t *testing.T) {
	k := New(t)
	k.DeliverTo(asyncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		time.Sleep(20 * time.Millisecond)
		echoHandler(ctx, b, u)
	}).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")

	if reply := user.ExpectReply(); reply.Text != "echo: hi" {
		t.Errorf("reply = %+v, want the echo the handler sent late", reply)
	}
}

func TestExpectReplyWalksRepliesInOrder(t *testing.T) {
	k := New(t)
	k.DeliverTo(asyncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		for _, text := range []string{"first", "second"} {
			b.SendMessage(ctx, &bot.SendMessageParams{ChatID: u.Message.Chat.ID, Text: text})
		}
	}).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")

	if reply := user.ExpectReply(); reply.Text != "first" {
		t.Errorf("first reply = %q, want the earlier message", reply.Text)
	}
	if reply := user.ExpectReply(); reply.Text != "second" {
		t.Errorf("second reply = %q, want the later message", reply.Text)
	}
}

// A reply already sitting in the chat answers what the user did before it, so
// the next verb only ever waits for something newer.
func TestExpectReplyWaitsForTheAnswerToTheLastVerb(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Tap("English")

	if reply := user.ExpectReply(); reply.Text != "tapped: lang:en" {
		t.Errorf("reply = %q, want the answer to the tap, not the menu before it", reply.Text)
	}
}

func TestExpectReplyReportsASilentBot(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb, WithWaitTimeout(30*time.Millisecond))
	k.DeliverTo(func(context.Context, *models.Update) {})

	user := k.User(7)
	user.Send("hi")

	if reply := user.ExpectReply(); reply.Text != "" {
		t.Errorf("reply = %+v, want nothing from a bot that never answered", reply)
	}
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "a reply to user 7") {
		t.Errorf("errors = %v, want one naming what was waited for", errs)
	}
}

func TestWaitForWatchesALaterEdit(t *testing.T) {
	k := New(t)
	k.DeliverTo(asyncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		sent, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: u.Message.Chat.ID, Text: "working"})
		if err != nil {
			t.Errorf("SendMessage: %v", err)
			return
		}
		time.Sleep(10 * time.Millisecond)
		b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    u.Message.Chat.ID,
			MessageID: sent.ID,
			Text:      "done",
		})
	}).ProcessUpdate)

	user := k.User(7)
	user.Send("go")

	k.WaitFor("the placeholder to be replaced", func() bool { return user.Screen().Text == "done" })
	if text := user.Screen().Text; text != "done" {
		t.Errorf("screen = %q, want the edited text", text)
	}
}

func TestSettleWaitsForAPacedSender(t *testing.T) {
	k := New(t)
	k.DeliverTo(asyncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		for i := range 3 {
			time.Sleep(5 * time.Millisecond)
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: u.Message.Chat.ID,
				Text:   string(rune('a' + i)),
			})
		}
	}).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	k.Settle()

	if log := user.History(); len(log) != 4 {
		t.Errorf("history = %d entries, want the input and all three paced replies", len(log))
	}
}
