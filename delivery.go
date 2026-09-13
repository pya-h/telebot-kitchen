package kitchen

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
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

// deliver hands one update over, and then any the bot made while it was being
// handled: a bot approving a join request from inside its own handler would
// otherwise wait on a delivery that is waiting on the bot.
func (k *Kitchen) deliver(u models.Update) {
	if !k.deliverMu.TryLock() {
		k.waiting.add(u)
		return
	}
	defer k.deliverMu.Unlock()

	k.handOver(u)
	for next, more := k.waiting.take(); more; next, more = k.waiting.take() {
		k.handOver(next)
	}
}

func (k *Kitchen) handOver(u models.Update) {
	defer k.activity.note()

	if id, opened := opener(&u); opened {
		k.world.start(id)
	}

	// Read before the bot runs: its own API calls take this lock too.
	to := k.binding()

	if kind := kindOf(&u); kind != "" && !allows(to.allowed, kind) {
		k.reportDropped(kind)
		return
	}
	u.ID = k.world.nextUpdate()
	// Remembered only once somebody has taken it, so Redeliver never repeats an
	// update no bot ever saw.
	if k.hand(u, to) {
		k.last, k.delivered = u, true
	}
}

// waitingUpdates are the ones made while a delivery was already under way.
type waitingUpdates struct {
	mu   sync.Mutex
	next []models.Update
}

func (q *waitingUpdates) add(u models.Update) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.next = append(q.next, u)
}

func (q *waitingUpdates) take() (models.Update, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.next) == 0 {
		return models.Update{}, false
	}
	u := q.next[0]
	q.next = q.next[1:]
	return u, true
}

// binding is how the bot is attached to the kitchen, taken in one read.
type binding struct {
	process    UpdateProcessor
	hook       http.Handler
	registered webhook
	polling    bool
	wire       bool
	allowed    []string
}

// queued says the bot takes its updates rather than being handed them.
func (b binding) queued() bool { return b.polling || (b.wire && b.registered.url == "") }

func (k *Kitchen) binding() binding {
	k.mu.RLock()
	defer k.mu.RUnlock()

	to := binding{k.process, k.hook, k.webhook, k.polling, k.wire, k.allowed}
	// The declared token stands in only while the bot has registered nothing:
	// Telegram sends a secret only where setWebhook was given one.
	if to.registered.url == "" && to.registered.secretToken == "" {
		to.registered.secretToken = k.secret
	}
	return to
}

// Redeliver hands the bot its last update again, identical and under the same
// id, the way Telegram does when it is not sure the first one arrived. Nothing
// in the chats changes.
func (k *Kitchen) Redeliver() {
	k.deliverMu.Lock()
	defer k.deliverMu.Unlock()
	defer k.activity.note()

	if !k.delivered {
		k.tb.Errorf("kitchen: nothing has been delivered yet, so there is nothing to redeliver")
		return
	}

	to := k.binding()
	// Telegram hands a polling bot back no update its offset has confirmed, and
	// hands it the rest again anyway.
	if to.queued() {
		if k.updates.confirmed(k.last.ID) {
			k.tb.Errorf("kitchen: the bot's offset has already taken update %d, and Telegram redelivers none it has confirmed", k.last.ID)
			return
		}
		k.tb.Errorf("kitchen: the bot has not taken update %d yet, so its next poll is handed it again without Redeliver", k.last.ID)
		return
	}
	k.hand(k.last, to)
}

// hand gives the update to whatever is bound, and says whether anything took it.
func (k *Kitchen) hand(u models.Update, to binding) bool {
	switch {
	case to.hook != nil:
		k.post(to.hook, to.registered, u)
	case to.process != nil:
		to.process(context.Background(), &u)
	case to.wire && to.registered.url != "":
		k.send(to.registered, u)
	case to.polling || to.wire:
		k.updates.add(u)
	default:
		k.tb.Errorf("kitchen: no bot bound, call DeliverTo, DeliverToWebhook, DeliverByPolling or DeliverOverHTTP first")
		return false
	}
	return true
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

// The kinds Telegram names that no kitchen verb makes. A bot may still register
// them, and listing them keeps a real name from reading as a typo.
var undelivered = []string{
	"business_connection", "business_message", "edited_business_message",
	"deleted_business_messages", "shipping_query", "purchased_paid_media",
}

var everyUpdateKind = append(kindNames(), undelivered...)

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
