package kitchen

import "github.com/go-telegram/bot/models"

type webhook struct {
	url         string
	secretToken string
}

func (k *Kitchen) getMe(params) (any, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.bot, nil
}

func (k *Kitchen) setWebhook(p params) (any, error) {
	if err := k.registerKinds(p); err != nil {
		return nil, err
	}
	registered := webhook{url: p["url"], secretToken: p["secret_token"]}

	k.mu.Lock()
	defer k.mu.Unlock()
	if k.secret != "" && registered.secretToken != "" && registered.secretToken != k.secret {
		k.tb.Errorf("kitchen: the test declared the webhook secret %q, but the bot registered %q", k.secret, registered.secretToken)
	}
	k.webhook = registered
	return true, nil
}

// register takes the bot's allowed_updates. An absent list keeps the previous
// setting, and an empty one is the default set.
func (k *Kitchen) registerKinds(p params) error {
	if _, given := p["allowed_updates"]; !given {
		return nil
	}
	var asked []string
	if err := p.decode("allowed_updates", &asked); err != nil {
		return badRequest("allowed_updates")
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	k.allowed = asked
	return nil
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
	return models.WebhookInfo{URL: k.webhook.url, AllowedUpdates: k.allowed}, nil
}
