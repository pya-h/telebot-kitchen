package kitchen

import "github.com/go-telegram/bot/models"

func (k *Kitchen) forwardMessage(p params) (any, error) {
	source, target, err := k.relayed(p, "forward")
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	forwarded := source
	forwarded.From = &sender
	forwarded.ForwardOrigin = origin(source)
	forwarded.EditDate = 0
	// Whose message it is now depends on where it lands, not on where it came from.
	forwarded.SenderChat = nil
	forwarded.AuthorSignature = ""
	forwarded.MediaGroupID = ""
	forwarded.ReplyMarkup = forwardable(source.ReplyMarkup)

	return k.relaid(target, forwarded), nil
}

func (k *Kitchen) copyMessage(p params) (any, error) {
	source, target, err := k.relayed(p, "copy")
	if err != nil {
		return nil, err
	}
	caption, entities, err := p.caption()
	if err != nil {
		return nil, err
	}
	markup, err := k.accept(p, target)
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	copied := source
	copied.From = &sender
	copied.EditDate = 0
	copied.SenderChat = nil
	copied.AuthorSignature = ""
	copied.MediaGroupID = ""
	copied.ForwardOrigin = nil
	copied.ReplyMarkup = markup
	if _, captioned := mediaOf(&copied); captioned && caption != "" {
		copied.Caption, copied.CaptionEntities = caption, entities
	}

	sent := k.relaid(target, copied)
	return models.MessageID{ID: sent.ID}, nil
}

func (k *Kitchen) relayed(p params, what string) (source models.Message, target int64, err error) {
	target, err = p.chatID()
	if err != nil {
		return models.Message{}, 0, err
	}
	from, err := p.fromChatID()
	if err != nil {
		return models.Message{}, 0, err
	}
	messageID, err := p.messageID()
	if err != nil {
		return models.Message{}, 0, err
	}

	source, found := k.world.message(from, messageID)
	if !found {
		return models.Message{}, 0, requestError("message to " + what + " not found")
	}
	if err := k.mayPost(target); err != nil {
		return models.Message{}, 0, err
	}
	return source, target, nil
}

func origin(m models.Message) *models.MessageOrigin {
	switch {
	// Forwarding a forward still points at whoever wrote it. Copied, because the
	// library writes the type back into the struct as it encodes one.
	case m.ForwardOrigin != nil:
		carried := *m.ForwardOrigin
		return &carried

	case m.SenderChat != nil && m.SenderChat.Type == models.ChatTypeChannel:
		return &models.MessageOrigin{
			Type: models.MessageOriginTypeChannel,
			MessageOriginChannel: &models.MessageOriginChannel{
				Date:            m.Date,
				Chat:            *m.SenderChat,
				MessageID:       m.ID,
				AuthorSignature: signature(m.AuthorSignature),
			},
		}

	// An admin speaking as the group itself.
	case m.SenderChat != nil:
		return &models.MessageOrigin{
			Type: models.MessageOriginTypeChat,
			MessageOriginChat: &models.MessageOriginChat{
				Date:            m.Date,
				SenderChat:      *m.SenderChat,
				AuthorSignature: signature(m.AuthorSignature),
			},
		}

	case m.From != nil:
		return &models.MessageOrigin{
			Type: models.MessageOriginTypeUser,
			MessageOriginUser: &models.MessageOriginUser{
				Date:       m.Date,
				SenderUser: *m.From,
			},
		}
	}
	return nil
}

func signature(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// forwardable is the keyboard a forward keeps. Telegram lets a link or a copy
// button through; any button that would act for the bot takes the whole keyboard
// down with it.
func forwardable(markup *models.InlineKeyboardMarkup) *models.InlineKeyboardMarkup {
	if markup == nil {
		return nil
	}
	rows := make([][]models.InlineKeyboardButton, len(markup.InlineKeyboard))
	for i, row := range markup.InlineKeyboard {
		rows[i] = make([]models.InlineKeyboardButton, len(row))
		for j, button := range row {
			kept := models.InlineKeyboardButton{Text: button.Text, Style: button.Style, IconCustomEmojiID: button.IconCustomEmojiID}
			switch {
			case button.URL != "":
				kept.URL = button.URL
			case button.LoginURL != nil:
				kept.URL = button.LoginURL.URL
			case button.CopyText != nil:
				copyText := *button.CopyText
				kept.CopyText = &copyText
			default:
				return nil
			}
			rows[i][j] = kept
		}
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
