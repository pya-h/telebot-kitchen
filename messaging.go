package kitchen

import (
	"reflect"

	"github.com/go-telegram/bot/models"
)

var errNotModified = requestError("message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message")

func (k *Kitchen) sendMessage(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	text := p["text"]
	if text == "" {
		return nil, requestError("message text is empty")
	}
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	return k.world.add(chatID, models.Message{From: &sender, Text: text, ReplyMarkup: markup}), nil
}

func (k *Kitchen) sendPhoto(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	photo := p["photo"]
	if photo == "" {
		return nil, badRequest("photo")
	}
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	return k.world.add(chatID, models.Message{
		From:        &sender,
		Photo:       k.files.photoSizes(photo),
		Caption:     p["caption"],
		ReplyMarkup: markup,
	}), nil
}

func (k *Kitchen) editMessageText(p params) (any, error) {
	text := p["text"]
	if text == "" {
		return nil, requestError("message text is empty")
	}
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	return k.applyEdit(p, func(m *models.Message) bool {
		if m.Text == text && sameMarkup(m.ReplyMarkup, markup) {
			return false
		}
		m.Text, m.ReplyMarkup = text, markup
		return true
	})
}

func (k *Kitchen) editMessageCaption(p params) (any, error) {
	caption := p["caption"]
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	return k.applyEdit(p, func(m *models.Message) bool {
		if m.Caption == caption && sameMarkup(m.ReplyMarkup, markup) {
			return false
		}
		m.Caption, m.ReplyMarkup = caption, markup
		return true
	})
}

func (k *Kitchen) editMessageReplyMarkup(p params) (any, error) {
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	return k.applyEdit(p, func(m *models.Message) bool {
		if sameMarkup(m.ReplyMarkup, markup) {
			return false
		}
		m.ReplyMarkup = markup
		return true
	})
}

func (k *Kitchen) deleteMessage(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	messageID, err := p.messageID()
	if err != nil {
		return nil, err
	}
	if !k.world.remove(chatID, messageID) {
		return nil, requestError("message to delete not found")
	}
	return true, nil
}

func (k *Kitchen) answerCallbackQuery(p params) (any, error) {
	queryID := p["callback_query_id"]
	if queryID == "" {
		return nil, badRequest("callback_query_id")
	}

	k.callbacks.record(CallbackAnswer{
		QueryID:   queryID,
		Text:      p["text"],
		ShowAlert: p.flag("show_alert"),
		URL:       p["url"],
		CacheTime: p.number("cache_time"),
	})
	return true, nil
}

func (k *Kitchen) applyEdit(p params, mutate func(*models.Message) bool) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	messageID, err := p.messageID()
	if err != nil {
		return nil, err
	}

	m, changed, ok := k.world.edit(chatID, messageID, mutate)
	if !ok {
		return nil, requestError("message to edit not found")
	}
	if !changed {
		return nil, errNotModified
	}
	return m, nil
}

func (k *Kitchen) botUser() models.User {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.bot
}

func sameMarkup(a, b *models.InlineKeyboardMarkup) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(a.InlineKeyboard, b.InlineKeyboard)
}
