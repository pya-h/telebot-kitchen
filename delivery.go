package kitchen

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	u.ID = k.world.nextUpdate()
	if id, opened := opener(&u); opened {
		k.world.start(id)
	}

	// Released before the bot runs: its own API calls take this lock too.
	k.mu.RLock()
	process, hook, polling, wire := k.process, k.hook, k.polling, k.wire
	registered := k.webhook
	k.mu.RUnlock()

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
	// The token the bot registered itself, so one that checks it always passes.
	if registered.secretToken != "" {
		req.Header.Set(secretTokenHeader, registered.secretToken)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if ctx.Err() != nil {
		k.tb.Errorf("kitchen: update %d was not accepted within %s, is the bot consuming its webhook?", u.ID, deliveryTimeout)
	}
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
