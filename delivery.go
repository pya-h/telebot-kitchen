package kitchen

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/go-telegram/bot/models"
)

const (
	secretTokenHeader = "X-Telegram-Bot-Api-Secret-Token"

	deliveryTimeout = 5 * time.Second
)

// UpdateProcessor is a bot's "handle one update" entry point.
type UpdateProcessor func(context.Context, *models.Update)

func (k *Kitchen) DeliverTo(process UpdateProcessor) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.process, k.hook = process, nil
}

// DeliverToWebhook posts updates to the bot's webhook handler, in process.
func (k *Kitchen) DeliverToWebhook(handler http.Handler) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.hook, k.process = handler, nil
}

func (k *Kitchen) deliver(u models.Update) {
	k.deliverMu.Lock()
	defer k.deliverMu.Unlock()

	u.ID = k.world.nextUpdate()

	// Released before the bot runs: its own API calls take this lock too.
	k.mu.RLock()
	process, hook := k.process, k.hook
	registered := k.webhook
	k.mu.RUnlock()

	switch {
	case hook != nil:
		k.post(hook, registered, u)
	case process != nil:
		process(context.Background(), &u)
	default:
		k.tb.Errorf("kitchen: no bot bound, call DeliverTo or DeliverToWebhook first")
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
	// The token the bot itself registered, so a bot that checks it always passes.
	if registered.secretToken != "" {
		req.Header.Set(secretTokenHeader, registered.secretToken)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if ctx.Err() != nil {
		k.tb.Errorf("kitchen: update %d was not accepted within %s, is the bot consuming its webhook?", u.ID, deliveryTimeout)
	}
}
