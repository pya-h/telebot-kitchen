package kitchen

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

	select {
	case <-replied:
	case <-time.After(2 * time.Second):
		t.Fatal("the bot never saw the update, so the declared secret did not reach it")
	}
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
	if u.Message == nil { // membership updates arrive on the same handler
		return
	}
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

func deliveredKinds(seen []models.Update) []string {
	kinds := make([]string, len(seen))
	for i := range seen {
		kinds[i] = kindOf(&seen[i])
	}
	return kinds
}

func TestAnUpdateKindTheBotDidNotAskForIsDropped(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	var got updates
	got.collect(k)
	team := k.Group(-42, "Standup")
	ada := k.User(7).In(team)

	ada.Join()                  // the service message, then chat_member
	ada.React("\U0001F44D")     // message_reaction, in a group the bot administers
	ada.PromoteBot(PinMessages) // my_chat_member, which is in the default set

	seen := got.all()
	if kinds := deliveredKinds(seen); !slices.Equal(kinds, []string{"message", "my_chat_member"}) {
		t.Errorf("kinds = %v, want only what the default set carries", kinds)
	}
	// A dropped update is not an update, so it takes no id with it.
	if seen[0].ID != 1 || seen[1].ID != 2 {
		t.Errorf("ids = %d, %d; want them consecutive, with no gap where a drop was", seen[0].ID, seen[1].ID)
	}
	notes := tb.noted()
	if len(notes) != 2 || !strings.Contains(notes[0], "dropped chat_member") ||
		!strings.Contains(notes[1], "dropped message_reaction") {
		t.Fatalf("notes = %q, want one per kind dropped", notes)
	}

	ada.Leave()
	if again := tb.noted(); len(again) != len(notes) {
		t.Errorf("notes = %q, want a kind said once however often it is dropped", again)
	}
}

func TestTheBotHearsTheKindsItRegisteredFor(t *testing.T) {
	k := New(t)
	var got updates
	got.collect(k)
	ada := k.User(7).In(k.Group(-42, "Standup"))

	callJSON(t, k, "setWebhook", `{"url":"https://example.test/hook","allowed_updates":["chat_member"]}`)
	ada.Join()

	var registered models.WebhookInfo
	callJSON(t, k, "getWebhookInfo", `{}`).decode(t, &registered)
	if !slices.Equal(registered.AllowedUpdates, []string{"chat_member"}) {
		t.Errorf("allowed updates = %v, want the registered list read back", registered.AllowedUpdates)
	}

	// An absent list keeps the setting, an empty one is the default set again.
	callJSON(t, k, "setWebhook", `{"url":"https://example.test/hook"}`)
	ada.Leave()
	callJSON(t, k, "setWebhook", `{"url":"https://example.test/hook","allowed_updates":[]}`)
	ada.Join()

	want := []string{"chat_member", "chat_member", "message"}
	if kinds := deliveredKinds(got.all()); !slices.Equal(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	var reset models.WebhookInfo
	callJSON(t, k, "getWebhookInfo", `{}`).decode(t, &reset)
	if len(reset.AllowedUpdates) != 0 {
		t.Errorf("allowed updates = %v, want the empty list the bot last sent", reset.AllowedUpdates)
	}
}

func TestAPollingBotRegistersItsKindsToo(t *testing.T) {
	k := New(t, WithWaitTimeout(50*time.Millisecond))
	k.DeliverByPolling()
	callForm(t, k, "getUpdates", map[string]string{"allowed_updates": `["chat_member"]`})

	k.User(7).In(k.Group(-42, "Standup")).Join()

	queued := k.updates.peek(0, mostUpdates)
	if kinds := deliveredKinds(queued); !slices.Equal(kinds, []string{"chat_member"}) {
		t.Errorf("queued = %v, want only the kind the poll asked for", kinds)
	}
}

func TestTheKindsCanBeDeclaredWithoutTheBotRegistering(t *testing.T) {
	k := New(t, WithAllowedUpdates("chat_member"))
	var got updates
	got.collect(k)

	k.User(7).In(k.Group(-42, "Standup")).Join()

	if kinds := deliveredKinds(got.all()); !slices.Equal(kinds, []string{"chat_member"}) {
		t.Errorf("kinds = %v, want only the declared kind", kinds)
	}
}

func TestAnUpdateKindTelegramDoesNotHaveIsRefused(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	New(tb, WithAllowedUpdates("chat_membre"))

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "no update is a") {
		t.Errorf("errors = %q, want one about the kind", errs)
	}
}

func TestADeclaredWebhookSecretReachesABotThatChecksIt(t *testing.T) {
	k := New(t, WithWebhookSecret("s3cret"))
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

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go b.StartWebhook(ctx)

	k.DeliverToWebhook(b.WebhookHandler())
	k.deliver(textUpdate(testChatID, "hi"))

	select {
	case <-replied:
	case <-time.After(2 * time.Second):
		t.Fatal("the bot never saw the update, so the declared secret did not reach it")
	}
	if reply, ok := k.world.latest(testChatID); !ok || reply.Text != "echo: hi" {
		t.Errorf("reply = %+v, %v; want the echo, so the update carried the declared secret", reply, ok)
	}
}

func TestABotRegisteringAnotherSecretFailsTheTest(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb, WithWebhookSecret("s3cret"))
	callJSON(t, k, "setWebhook", `{"url":"https://example.test/hook","secret_token":"other"}`)

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], `"s3cret"`) || !strings.Contains(errs[0], `"other"`) {
		t.Fatalf("errors = %q, want one naming both secrets", errs)
	}
	callJSON(t, k, "setWebhook", `{"url":"https://example.test/hook","secret_token":"s3cret"}`)
	if again := tb.errors(); len(again) != len(errs) {
		t.Errorf("errors = %q, want nothing said about the secret the test declared", again)
	}
}

func TestDeliveringWithoutASecretIsSaidOnce(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverToWebhook(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	k.deliver(textUpdate(testChatID, "one"))
	k.deliver(textUpdate(testChatID, "two"))

	notes := tb.noted()
	if len(notes) != 1 || !strings.Contains(notes[0], "without a secret token") {
		t.Errorf("notes = %q, want one naming the trap", notes)
	}
}

// Telegram names update kinds the kitchen has no verb for. A bot may register
// them, and nothing arrives for them, which is what production does too.
func TestAKindTheKitchenNeverSendsMayStillBeRegistered(t *testing.T) {
	k := New(t, WithAllowedUpdates("message", "shipping_query"))
	var got updates
	got.collect(k)

	k.User(7).Send("hi")

	if kinds := deliveredKinds(got.all()); !slices.Equal(kinds, []string{"message"}) {
		t.Errorf("kinds = %v, want the message and nothing else", kinds)
	}
}

// The bot answering from inside its own handler makes an update of its own. The
// kitchen must not be waiting on the handler that is waiting on the kitchen.
func TestABotMayAnswerFromInsideItsHandler(t *testing.T) {
	k := New(t, alsoHearing("chat_member"))
	var b *bot.Bot
	b = syncBot(t, k, func(ctx context.Context, _ *bot.Bot, u *models.Update) {
		if u.ChatJoinRequest == nil {
			return
		}
		if _, err := b.ApproveChatJoinRequest(ctx, &bot.ApproveChatJoinRequestParams{
			ChatID: u.ChatJoinRequest.Chat.ID, UserID: u.ChatJoinRequest.From.ID,
		}); err != nil {
			t.Errorf("ApproveChatJoinRequest: %v", err)
		}
	})
	k.DeliverTo(b.ProcessUpdate)
	team := k.Supergroup(-1001, "Standup")

	asked := make(chan struct{})
	go func() {
		defer close(asked)
		k.User(7).In(team).AskToJoin("let me in")
	}()
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the join request never came back; the kitchen waited on a bot that was waiting on it")
	}
	k.Settle()

	if members := team.Members(); len(members) != 1 || members[0].ID() != 7 {
		t.Errorf("roster = %+v, want the requester let in", members)
	}
}

func TestAnUpdateNobodyTookIsNotRedelivered(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.deliver(textUpdate(testChatID, "hi"))
	k.Redeliver()

	errs := tb.errors()
	if len(errs) != 2 || !strings.Contains(errs[0], "no bot bound") ||
		!strings.Contains(errs[1], "nothing has been delivered") {
		t.Errorf("errors = %q, want the unbound delivery and then nothing to redeliver", errs)
	}
}

// The declared token stands in for a bot that registers nothing. Once the bot
// registers a webhook without one, Telegram would send no secret, and neither
// does the kitchen — which is the trap, said out loud.
func TestADeclaredSecretStandsOnlyUntilTheBotRegisters(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb, WithWebhookSecret("s3cret"))
	var carried []string
	k.DeliverToWebhook(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		carried = append(carried, r.Header.Get(secretTokenHeader))
	}))

	k.deliver(textUpdate(testChatID, "one"))
	callJSON(t, k, "setWebhook", `{"url":"https://example.test/hook"}`)
	k.deliver(textUpdate(testChatID, "two"))

	if !slices.Equal(carried, []string{"s3cret", ""}) {
		t.Errorf("secrets carried = %q, want the declared one until the bot registered without it", carried)
	}
	if notes := tb.noted(); len(notes) != 1 || !strings.Contains(notes[0], "without a secret token") {
		t.Errorf("notes = %q, want the trap named once", notes)
	}
	if errs := tb.errors(); len(errs) != 0 {
		t.Errorf("errors = %q, want registering no secret to be no mismatch", errs)
	}
}

func TestTheDefaultSetIsWhatTelegramHoldsBack(t *testing.T) {
	kinds := DefaultUpdates()
	for _, held := range []string{"chat_member", "message_reaction", "message_reaction_count"} {
		if slices.Contains(kinds, held) {
			t.Errorf("%s is in the default set, want it held back until a bot asks", held)
		}
	}
	for _, sent := range []string{"message", "callback_query", "my_chat_member", "chat_join_request"} {
		if !slices.Contains(kinds, sent) {
			t.Errorf("%s is not in the default set, want a bot to get it without asking", sent)
		}
	}
}
