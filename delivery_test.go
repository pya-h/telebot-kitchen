package kitchen

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestDirectDeliveryIsOrdered(t *testing.T) {
	k := New(t)

	var got []string
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		got = append(got, u.Message.Text)
		if want := int64(len(got)); u.ID != want {
			t.Errorf("update id = %d, want %d", u.ID, want)
		}
	})

	for _, text := range []string{"one", "two", "three"} {
		k.deliver(textUpdate(testChatID, text))
	}
	if strings.Join(got, ",") != "one,two,three" {
		t.Errorf("delivered = %v, want them in order", got)
	}
}

// With synchronous handlers the bot's reply is already sent when delivery returns.
func TestDirectDeliveryRunsSynchronousHandlers(t *testing.T) {
	k := New(t)
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()), bot.WithNotAsyncHandlers(),
		bot.WithDefaultHandler(echoHandler))
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}
	k.DeliverTo(b.ProcessUpdate)

	k.deliver(textUpdate(testChatID, "hi"))

	reply, ok := k.world.latest(testChatID)
	if !ok || reply.Text != "echo: hi" {
		t.Errorf("reply = %+v, %v; want the echo already sent", reply, ok)
	}
}

func TestWebhookDeliveryReachesBot(t *testing.T) {
	k := New(t)
	replied := make(chan struct{})
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()),
		bot.WithWebhookSecretToken("s3cret"),
		bot.WithDefaultHandler(func(ctx context.Context, b *bot.Bot, u *models.Update) {
			echoHandler(ctx, b, u)
			close(replied)
		}))
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}
	if _, err := b.SetWebhook(context.Background(), &bot.SetWebhookParams{
		URL:         "https://example.test/hook",
		SecretToken: "s3cret",
	}); err != nil {
		t.Fatalf("SetWebhook: %v", err)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go b.StartWebhook(ctx)

	k.DeliverToWebhook(b.WebhookHandler())
	k.deliver(textUpdate(testChatID, "hi"))

	<-replied
	if reply, ok := k.world.latest(testChatID); !ok || reply.Text != "echo: hi" {
		t.Errorf("reply = %+v, %v; want the echo", reply, ok)
	}
}

func TestWebhookDeliveryCarriesRegisteredSecret(t *testing.T) {
	k := New(t)
	callJSON(t, k, "setWebhook", `{"url":"https://example.test/hook","secret_token":"s3cret"}`)

	var mu sync.Mutex
	var secret, path string
	k.DeliverToWebhook(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		secret, path = r.Header.Get(secretTokenHeader), r.URL.Path
	}))
	k.deliver(textUpdate(testChatID, "hi"))

	mu.Lock()
	defer mu.Unlock()
	if secret != "s3cret" {
		t.Errorf("secret token = %q, want the registered one", secret)
	}
	if path != "/hook" {
		t.Errorf("path = %q, want the registered webhook path", path)
	}
}

func TestDeliveryWithoutABoundBotReports(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	defer tb.close()

	k.deliver(textUpdate(testChatID, "hi"))

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "no bot bound") {
		t.Errorf("reported errors = %v, want one naming the missing binding", errs)
	}
}

func TestBindingOneModeClearsTheOther(t *testing.T) {
	k := New(t)
	k.DeliverToWebhook(http.NotFoundHandler())
	k.DeliverTo(func(context.Context, *models.Update) {})

	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.hook != nil {
		t.Error("webhook handler survived a direct binding")
	}
}

func echoHandler(ctx context.Context, b *bot.Bot, u *models.Update) {
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: u.Message.Chat.ID,
		Text:   "echo: " + u.Message.Text,
	})
}

func textUpdate(chatID int64, text string) models.Update {
	return models.Update{Message: &models.Message{
		From: &models.User{ID: chatID, FirstName: "Tester"},
		Chat: models.Chat{ID: chatID, Type: models.ChatTypePrivate},
		Text: text,
	}}
}
