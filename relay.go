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
	// Telegram strips inline keyboards on a forward
	forwarded.ReplyMarkup = nil

	return k.world.add(target, forwarded), nil
}

func (k *Kitchen) copyMessage(p params) (any, error) {
	source, target, err := k.relayed(p, "copy")
	if err != nil {
		return nil, err
	}
	markup, err := p.markup()
	if err != nil {
		return nil, err
	}

	sender := k.botUser()
	copied := source
	copied.From = &sender
	copied.EditDate = 0
	copied.SenderChat = nil
	copied.ForwardOrigin = nil
	copied.ReplyMarkup = markup
	if caption := p["caption"]; caption != "" && len(copied.Photo) > 0 {
		copied.Caption = caption
	}

	sent := k.world.add(target, copied)
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
	if err := k.world.mayPost(target); err != nil {
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

	case m.SenderChat != nil:
		return &models.MessageOrigin{
			Type: models.MessageOriginTypeChannel,
			MessageOriginChannel: &models.MessageOriginChannel{
				Date:      m.Date,
				Chat:      *m.SenderChat,
				MessageID: m.ID,
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
