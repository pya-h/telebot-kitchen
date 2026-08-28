package kitchen

import "github.com/go-telegram/bot/models"

type webhook struct {
	url            string
	secretToken    string
	allowedUpdates []string
}

func (k *Kitchen) getMe(params) (any, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.bot, nil
}

func (k *Kitchen) setWebhook(p params) (any, error) {
	registered := webhook{url: p["url"], secretToken: p["secret_token"]}
	if err := p.decode("allowed_updates", &registered.allowedUpdates); err != nil {
		return nil, badRequest("allowed_updates")
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	k.webhook = registered
	return true, nil
}

func (k *Kitchen) deleteWebhook(params) (any, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.webhook = webhook{}
	return true, nil
}

func (k *Kitchen) getWebhookInfo(params) (any, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return models.WebhookInfo{URL: k.webhook.url, AllowedUpdates: k.webhook.allowedUpdates}, nil
}

func badRequest(field string) *apiError {
	return &apiError{Code: 400, Description: "Bad Request: can't parse field " + field}
}
