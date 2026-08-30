package kitchen

import (
	"context"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestUserTextReachesTheBot(t *testing.T) {
	k := New(t)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)

	user := k.User(7, WithFullName("Ali", "Rezaei"), WithUsername("ali"), WithLanguage("fa"))
	user.Send("سلام")

	log := k.world.history(user.ChatID())
	if len(log) != 2 {
		t.Fatalf("history = %+v, want the user's message and the reply", log)
	}

	sent := log[0]
	if sent.Text != "سلام" || sent.From == nil || sent.From.Username != "ali" {
		t.Errorf("sent = %+v, want the user's own message", sent)
	}
	if sent.From.LanguageCode != "fa" {
		t.Errorf("language = %q, want the configured one", sent.From.LanguageCode)
	}
	if sent.Chat.ID != 7 || sent.Chat.FirstName != "Ali" || sent.Chat.Username != "ali" {
		t.Errorf("chat = %+v, want the user's private chat", sent.Chat)
	}
	if log[1].Text != "echo: سلام" {
		t.Errorf("reply = %q, want the echo", log[1].Text)
	}
}

func TestSendCommandCarriesEntity(t *testing.T) {
	k := New(t)
	var got *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = u.Message })

	k.User(7).SendCommand("settings", "lang", "fa")

	if got.Text != "/settings lang fa" {
		t.Fatalf("text = %q, want the command with its arguments", got.Text)
	}
	want := models.MessageEntity{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: 9}
	if len(got.Entities) != 1 || got.Entities[0] != want {
		t.Errorf("entities = %+v, want one covering %q", got.Entities, "/settings")
	}
}

func TestSendCommandAcceptsALeadingSlash(t *testing.T) {
	k := New(t)
	var got *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = u.Message })

	k.User(7).SendCommand("/start")

	if got.Text != "/start" || got.Entities[0].Length != 6 {
		t.Errorf("message = %+v, want a single /start command", got)
	}
}

// Telegram counts entity lengths in UTF-16 code units, not bytes or runes.
func TestEntityLengthCountsUTF16(t *testing.T) {
	cases := map[string]int{
		"/start": 6,
		"/شروع":  5,
		"/👍":     3,
	}
	for text, want := range cases {
		if got := utf16Len(text); got != want {
			t.Errorf("utf16Len(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestUserOptionsAreAdditive(t *testing.T) {
	k := New(t)
	k.User(7, WithUsername("ali"))
	again := k.User(7, WithLanguage("fa"))

	if again.info.Username != "ali" || again.info.LanguageCode != "fa" {
		t.Errorf("user = %+v, want both settings kept", again.info)
	}
	if first := k.User(7); first != again {
		t.Error("the same id produced a second user")
	}
}

func syncBot(t *testing.T, k *Kitchen, handler bot.HandlerFunc) *bot.Bot {
	t.Helper()
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()),
		bot.WithNotAsyncHandlers(), bot.WithDefaultHandler(handler))
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}
	return b
}
