package kitchen

import (
	"reflect"
	"slices"

	"github.com/go-telegram/bot/models"
)

var errNotModified = requestError("message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message")

func (k *Kitchen) sendMessage(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	text, entities, err := p.styled("text", "entities")
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, requestError("message text is empty")
	}
	markup, err := k.accept(p, chatID)
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	return k.world.add(chatID, models.Message{
		From: &sender, Text: text, Entities: entities, ReplyMarkup: markup,
	}), nil
}

// accept clears a send to go ahead, handing back the inline keyboard it carries.
// The hard keyboard is chat state, so it is only touched once the post is
// allowed: a refused send leaves the one already up alone.
func (k *Kitchen) accept(p params, chatID int64) (*models.InlineKeyboardMarkup, error) {
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}
	menu, changed, err := p.menu()
	if err != nil {
		return nil, err
	}
	if err := k.world.mayPost(chatID); err != nil {
		return nil, err
	}
	if changed {
		k.world.setMenu(chatID, menu)
	}
	return markup, nil
}

func (k *Kitchen) editMessageText(p params) (any, error) {
	text, entities, err := p.styled("text", "entities")
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, requestError("message text is empty")
	}
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	return k.applyEdit(p, func(_ *chat, m *models.Message) error {
		if label, _ := mediaOf(m); label != "" {
			return requestError("there is no text in the message to edit")
		}
		if m.Text == text && slices.Equal(m.Entities, entities) && sameMarkup(m.ReplyMarkup, markup) {
			return errNotModified
		}
		m.Text, m.Entities, m.ReplyMarkup = text, entities, markup
		return nil
	})
}

func (k *Kitchen) editMessageCaption(p params) (any, error) {
	caption, entities, err := p.styled("caption", "caption_entities")
	if err != nil {
		return nil, err
	}
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	return k.applyEdit(p, func(_ *chat, m *models.Message) error {
		if _, captioned := mediaOf(m); !captioned {
			return requestError("there is no caption in the message to edit")
		}
		if m.Caption == caption && slices.Equal(m.CaptionEntities, entities) && sameMarkup(m.ReplyMarkup, markup) {
			return errNotModified
		}
		m.Caption, m.CaptionEntities, m.ReplyMarkup = caption, entities, markup
		return nil
	})
}

func (k *Kitchen) editMessageMedia(p params) (any, error) {
	var replacement struct {
		Type            string                 `json:"type"`
		Media           string                 `json:"media"`
		Caption         string                 `json:"caption"`
		ParseMode       string                 `json:"parse_mode"`
		CaptionEntities []models.MessageEntity `json:"caption_entities"`
	}
	if err := p.decode("media", &replacement); err != nil || replacement.Media == "" {
		return nil, badRequest("media")
	}
	put, editable := editableKinds[replacement.Type]
	if !editable {
		return nil, requestError("type of the media to edit is not supported")
	}
	caption, entities, err := styledText(replacement.Caption, replacement.ParseMode, replacement.CaptionEntities)
	if err != nil {
		return nil, err
	}
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	file, err := k.files.resolve(p.attached(replacement.Media), replacement.Type)
	if err != nil {
		return nil, err
	}
	return k.applyEdit(p, func(_ *chat, m *models.Message) error {
		if label, _ := mediaOf(m); label == "" || m.Location != nil {
			return requestError("there is no media in the message to edit")
		}
		if fileIn(m) == file.ID && m.Caption == caption &&
			slices.Equal(m.CaptionEntities, entities) && sameMarkup(m.ReplyMarkup, markup) {
			return errNotModified
		}
		clearMedia(m)
		put(m, file)
		m.Caption, m.CaptionEntities, m.ReplyMarkup = caption, entities, markup
		return nil
	})
}

func (k *Kitchen) editMessageReplyMarkup(p params) (any, error) {
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	return k.applyEdit(p, func(_ *chat, m *models.Message) error {
		if sameMarkup(m.ReplyMarkup, markup) {
			return errNotModified
		}
		m.ReplyMarkup = markup
		return nil
	})
}

var chatActions = []string{
	"typing", "upload_photo", "record_video", "upload_video", "record_voice",
	"upload_voice", "upload_document", "choose_sticker", "find_location",
	"record_video_note", "upload_video_note",
}

func (k *Kitchen) sendChatAction(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	if !slices.Contains(chatActions, p["action"]) {
		return nil, requestError("wrong parameter action in request")
	}
	if err := k.world.mayPost(chatID); err != nil {
		return nil, err
	}
	return true, nil
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
	found, err := k.world.remove(chatID, messageID, k.botUser().ID)
	if !found {
		return nil, requestError("message to delete not found")
	}
	if err != nil {
		return nil, err
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

func (k *Kitchen) applyEdit(p params, mutate func(*chat, *models.Message) error) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	messageID, err := p.messageID()
	if err != nil {
		return nil, err
	}

	botID := k.botUser().ID
	m, found, err := k.world.edit(chatID, messageID, func(c *chat, m *models.Message) error {
		if err := c.mayEdit(m, botID); err != nil {
			return err
		}
		return mutate(c, m)
	})
	if !found {
		return nil, requestError("message to edit not found")
	}
	if err != nil {
		return nil, err
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
