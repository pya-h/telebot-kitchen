package kitchen

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"time"

	"github.com/go-telegram/bot/models"
)

const (
	secretTokenHeader = "X-Telegram-Bot-Api-Secret-Token"

	deliveryTimeout = 5 * time.Second
)

// bot's "handle one update" entry point.
type UpdateProcessor func(context.Context, *models.Update)

func (k *Kitchen) DeliverTo(process UpdateProcessor) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.process, k.hook, k.polling, k.wire = process, nil, false, false
}

func (k *Kitchen) DeliverToJSON(process func(context.Context, []byte)) {
	k.DeliverToWebhook(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		update, err := io.ReadAll(r.Body)
		if err != nil {
			k.tb.Errorf("kitchen: read update: %v", err)
			return
		}
		process(r.Context(), update)
	}))
}

func (k *Kitchen) DeliverToWebhook(handler http.Handler) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.hook, k.process, k.polling, k.wire = handler, nil, false, false
}

func (k *Kitchen) deliver(u models.Update) {
	k.deliverMu.Lock()
	defer k.deliverMu.Unlock()
	defer k.activity.note()

	if id, opened := opener(&u); opened {
		k.world.start(id)
	}

	// Released before the bot runs: its own API calls take this lock too.
	k.mu.RLock()
	process, hook, polling, wire := k.process, k.hook, k.polling, k.wire
	registered, asked := k.webhook, k.allowed
	if registered.secretToken == "" {
		registered.secretToken = k.secret
	}
	k.mu.RUnlock()

	if kind := kindOf(&u); kind != "" && !allows(asked, kind) {
		k.reportDropped(kind)
		return
	}
	u.ID = k.world.nextUpdate()

	switch {
	case hook != nil:
		k.post(hook, registered, u)
	case process != nil:
		process(context.Background(), &u)
	case wire && registered.url != "":
		k.send(registered, u)
	case polling || wire:
		k.updates.add(u)
	default:
		k.tb.Errorf("kitchen: no bot bound, call DeliverTo, DeliverToWebhook, DeliverByPolling or DeliverOverHTTP first")
	}
}

func (k *Kitchen) post(handler http.Handler, registered webhook, u models.Update) {
	body, err := json.Marshal(u)
	if err != nil {
		k.tb.Errorf("kitchen: encode update %d: %v", u.ID, err)
		return
	}

	url := registered.url
	if url == "" {
		url = "/"
	}

	ctx, cancel := context.WithTimeout(context.Background(), deliveryTimeout)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if registered.secretToken == "" {
		k.reportNoSecret()
	} else {
		req.Header.Set(secretTokenHeader, registered.secretToken)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if ctx.Err() != nil {
		k.tb.Errorf("kitchen: update %d was not accepted within %s, is the bot consuming its webhook?", u.ID, deliveryTimeout)
	}
}

type updateKind struct {
	name    string
	carried func(*models.Update) bool
}

var updateKinds = []updateKind{
	{"message", func(u *models.Update) bool { return u.Message != nil }},
	{"edited_message", func(u *models.Update) bool { return u.EditedMessage != nil }},
	{"channel_post", func(u *models.Update) bool { return u.ChannelPost != nil }},
	{"edited_channel_post", func(u *models.Update) bool { return u.EditedChannelPost != nil }},
	{"message_reaction", func(u *models.Update) bool { return u.MessageReaction != nil }},
	{"message_reaction_count", func(u *models.Update) bool { return u.MessageReactionCount != nil }},
	{"inline_query", func(u *models.Update) bool { return u.InlineQuery != nil }},
	{"chosen_inline_result", func(u *models.Update) bool { return u.ChosenInlineResult != nil }},
	{"callback_query", func(u *models.Update) bool { return u.CallbackQuery != nil }},
	{"pre_checkout_query", func(u *models.Update) bool { return u.PreCheckoutQuery != nil }},
	{"poll", func(u *models.Update) bool { return u.Poll != nil }},
	{"poll_answer", func(u *models.Update) bool { return u.PollAnswer != nil }},
	{"my_chat_member", func(u *models.Update) bool { return u.MyChatMember != nil }},
	{"chat_member", func(u *models.Update) bool { return u.ChatMember != nil }},
	{"chat_join_request", func(u *models.Update) bool { return u.ChatJoinRequest != nil }},
	{"chat_boost", func(u *models.Update) bool { return u.ChatBoost != nil }},
	{"removed_chat_boost", func(u *models.Update) bool { return u.RemovedChatBoost != nil }},
}

var everyUpdateKind = kindNames()

func kindNames() []string {
	names := make([]string, len(updateKinds))
	for i, kind := range updateKinds {
		names[i] = kind.name
	}
	return names
}

var notByDefault = []string{"chat_member", "message_reaction", "message_reaction_count"}

func DefaultUpdates() []string {
	kinds := make([]string, 0, len(everyUpdateKind))
	for _, name := range everyUpdateKind {
		if !slices.Contains(notByDefault, name) {
			kinds = append(kinds, name)
		}
	}
	return kinds
}

func kindOf(u *models.Update) string {
	for _, kind := range updateKinds {
		if kind.carried(u) {
			return kind.name
		}
	}
	return ""
}

func allows(asked []string, kind string) bool {
	if len(asked) == 0 {
		return !slices.Contains(notByDefault, kind)
	}
	return slices.Contains(asked, kind)
}

func (k *Kitchen) reportDropped(kind string) {
	if _, already := k.dropped.LoadOrStore(kind, true); !already {
		k.tb.Logf("kitchen: dropped %s: the bot did not ask for it (allowed_updates)", kind)
	}
}

func (k *Kitchen) reportNoSecret() {
	k.noSecret.Do(func() {
		k.tb.Logf("kitchen: delivering without a secret token; a bot built with WithWebhookSecretToken drops every such update without a trace")
	})
}

func opener(u *models.Update) (int64, bool) {
	// Blocking or unblocking the bot does not open the chat.
	if u.MyChatMember != nil {
		return 0, false
	}
	where, who, _ := about(u)
	if where == nil || who == nil || where.Type != models.ChatTypePrivate || where.ID != who.ID {
		return 0, false
	}
	return who.ID, true
}
