package kitchen

import "github.com/go-telegram/bot/models"

func (k *Kitchen) forwardMessage(p params) (any, error) {
	source, target, err := k.relayed(p, "forward")
	if err != nil {
		return nil, err
	}

	if !forwardsAtAll(source) {
		return nil, requestError("message can't be forwarded")
	}
	sender := k.botUser()
	forwarded := forwardedAs(source, k.originOf(source))
	forwarded.From = &sender
	return k.relaid(target, forwarded), nil
}

// forwardsAtAll is whether the message carries something of its own: a service
// message only says what happened in the chat, and Telegram forwards none.
func forwardsAtAll(m models.Message) bool {
	label, _ := mediaOf(&m)
	return m.Text != "" || label != "" || m.Invoice != nil
}

// forwardedAs is the message a forward lands as: the same content, under the
// origin it is credited to, sent by whoever forwards it.
func forwardedAs(source models.Message, from *models.MessageOrigin) models.Message {
	forwarded := source
	forwarded.ForwardOrigin = from
	forwarded.EditDate = 0
	// Whose message it is now depends on where it lands, not on where it came from.
	forwarded.SenderChat = nil
	forwarded.AuthorSignature = ""
	forwarded.MediaGroupID = ""
	forwarded.ReplyMarkup = forwardable(source.ReplyMarkup)
	return forwarded
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

// originOf is origin as the author allows it: one who hides their account when
// forwarded is credited by name alone.
func (k *Kitchen) originOf(m models.Message) *models.MessageOrigin {
	from := origin(m)
	if m.ForwardOrigin != nil || from == nil || from.MessageOriginUser == nil {
		return from
	}
	author := from.MessageOriginUser
	if !k.hidesForwards(author.SenderUser.ID) {
		return from
	}
	return &models.MessageOrigin{
		Type: models.MessageOriginTypeHiddenUser,
		MessageOriginHiddenUser: &models.MessageOriginHiddenUser{
			Date:           author.Date,
			SenderUserName: displayName(&author.SenderUser),
		},
	}
}

func origin(m models.Message) *models.MessageOrigin {
	switch {
	// Forwarding a forward still points at whoever wrote it.
	case m.ForwardOrigin != nil:
		return copyOrigin(m.ForwardOrigin)

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

// copyOrigin goes all the way down, because the library writes the type back
// into whichever part it encodes.
func copyOrigin(o *models.MessageOrigin) *models.MessageOrigin {
	if o == nil {
		return nil
	}
	out := *o
	switch {
	case o.MessageOriginUser != nil:
		part := *o.MessageOriginUser
		out.MessageOriginUser = &part
	case o.MessageOriginHiddenUser != nil:
		part := *o.MessageOriginHiddenUser
		out.MessageOriginHiddenUser = &part
	case o.MessageOriginChat != nil:
		part := *o.MessageOriginChat
		out.MessageOriginChat = &part
	case o.MessageOriginChannel != nil:
		part := *o.MessageOriginChannel
		out.MessageOriginChannel = &part
	}
	return &out
}
