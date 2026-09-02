package kitchen

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func send(b *bot.Bot, chatID int64, text string) error {
	_, err := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: chatID, Text: text})
	return err
}

func TestFloodWaitCarriesItsRetryAfter(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.Fail(TooManyRequests(3*time.Second), Method("sendMessage"))

	if err := send(b, testChatID, "hi"); err == nil || !strings.Contains(err.Error(), "retry after 3") {
		t.Errorf("err = %v, want the flood wait the kitchen was told to send", err)
	}
	if log := k.History(testChatID); len(log) != 0 {
		t.Errorf("history = %v, want a refused send to leave the chat untouched", log)
	}
}

func TestFailOnceLetsTheRetryThrough(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.FailOnce(ServerError(), Method("sendMessage"))

	if err := send(b, testChatID, "first"); err == nil {
		t.Fatal("first send: want the refusal")
	}
	if err := send(b, testChatID, "second"); err != nil {
		t.Fatalf("retry: %v", err)
	}

	log := k.History(testChatID)
	if len(log) != 1 || log[0].Text != "second" {
		t.Errorf("history = %v, want only the retry to have landed", log)
	}
}

func TestFailAfterSparesTheFirstCalls(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.FailAfter(2, ServerError(), Method("sendMessage"))

	for _, text := range []string{"one", "two"} {
		if err := send(b, testChatID, text); err != nil {
			t.Fatalf("send %q: %v", text, err)
		}
	}
	if err := send(b, testChatID, "three"); err == nil {
		t.Error("third send: want the refusal")
	}
}

func TestFaultsAreScopedToWhatTheyMatch(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.Fail(ServerError(), ToChat(testChatID))

	if err := send(b, testChatID, "hi"); err == nil {
		t.Error("the named chat: want the refusal")
	}
	if err := send(b, testChatID+1, "hi"); err != nil {
		t.Errorf("another chat: %v, want a fault to reach no further than its scope", err)
	}
}

func TestBrokenRepliesReachTheBotAsFailures(t *testing.T) {
	for name, fault := range map[string]Fault{"malformed": Malformed(), "dropped": Timeout()} {
		t.Run(name, func(t *testing.T) {
			k := New(t)
			b := newClient(t, k)
			k.Fail(fault, Method("sendMessage"))

			if err := send(b, testChatID, "hi"); err == nil {
				t.Error("want a reply the bot cannot read to surface as an error")
			}
		})
	}
}

func TestClearingAFaultRestoresTheKitchen(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	standing := k.Fail(ServerError(), Method("sendMessage"))
	if err := send(b, testChatID, "hi"); err == nil {
		t.Fatal("while the fault stands: want the refusal")
	}

	standing.Clear()
	if err := send(b, testChatID, "hi"); err != nil {
		t.Errorf("after Clear: %v", err)
	}

	k.Fail(ServerError())
	k.ClearFaults()
	if err := send(b, testChatID, "hi"); err != nil {
		t.Errorf("after ClearFaults: %v", err)
	}
}

func TestARefusedCallStaysOnTheRecord(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.Fail(TooManyRequests(time.Second), Method("sendMessage"))
	send(b, testChatID, "hi")

	call, ok := k.Calls().Last(Method("sendMessage"))
	if !ok || !strings.Contains(call.Error, "Too Many Requests") {
		t.Errorf("call = %+v, %v; want the refusal recorded against the attempt", call, ok)
	}
}

// The point of the whole section: a bot meeting a flood wait, and a test
// watching it come back.
func TestABotRecoversFromAFloodWait(t *testing.T) {
	k := New(t)
	k.FailOnce(TooManyRequests(0), Method("sendMessage"))
	k.DeliverTo(syncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		if err := send(b, u.Message.Chat.ID, "hi"); err != nil {
			send(b, u.Message.Chat.ID, "hi")
		}
	}).ProcessUpdate)

	user := k.User(7)
	user.Send("go")
	user.Expect(TextIs("hi"))
	k.ExpectCount(2, Method("sendMessage"))
}
