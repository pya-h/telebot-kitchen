package kitchen

import (
	"context"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestGetMeReportsConfiguredIdentity(t *testing.T) {
	k := New(t, WithToken("42:secret"), WithBotName("Chef"), WithBotUsername("chef_bot"))

	var me models.User
	callJSON(t, k, "getMe", "{}").decode(t, &me)

	want := models.User{ID: 42, IsBot: true, FirstName: "Chef", Username: "chef_bot"}
	if me != want {
		t.Errorf("getMe = %+v, want %+v", me, want)
	}
}

func TestWebhookLifecycle(t *testing.T) {
	k := New(t)

	callJSON(t, k, "setWebhook", `{"url":"https://example.test/hook","secret_token":"s3cret"}`)
	var info models.WebhookInfo
	callJSON(t, k, "getWebhookInfo", "{}").decode(t, &info)
	if info.URL != "https://example.test/hook" {
		t.Errorf("webhook url = %q, want the registered one", info.URL)
	}
	if k.webhook.secretToken != "s3cret" {
		t.Errorf("secret token = %q, want the registered one", k.webhook.secretToken)
	}

	callJSON(t, k, "deleteWebhook", "{}")
	callJSON(t, k, "getWebhookInfo", "{}").decode(t, &info)
	if info.URL != "" {
		t.Errorf("webhook url = %q after delete, want empty", info.URL)
	}
}

// A real client must boot against the kitchen unchanged: bot.New calls getMe.
func TestBotBootsAgainstKitchen(t *testing.T) {
	k := New(t)

	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}
	if _, err := b.SetWebhook(context.Background(), &bot.SetWebhookParams{
		URL:         "https://example.test/hook",
		SecretToken: "s3cret",
	}); err != nil {
		t.Fatalf("SetWebhook: %v", err)
	}

	info, err := b.GetWebhookInfo(context.Background())
	if err != nil {
		t.Fatalf("GetWebhookInfo: %v", err)
	}
	if info.URL != "https://example.test/hook" {
		t.Errorf("webhook url = %q, want the registered one", info.URL)
	}
}
