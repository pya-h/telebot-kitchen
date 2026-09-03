package kitchen

import "github.com/go-telegram/bot/models"

// The calls a bot makes before it acts, answered from the roster.
func (k *Kitchen) getChat(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	info, found := k.world.info(chatID)
	if !found {
		return nil, requestError("chat not found")
	}
	full := models.ChatFullInfo{
		ID:        info.ID,
		Type:      info.Type,
		Title:     info.Title,
		Username:  info.Username,
		FirstName: info.FirstName,
		LastName:  info.LastName,
	}
	if pinned, ok := k.world.newestPin(chatID); ok {
		full.PinnedMessage = &pinned
	}
	return full, nil
}

func (k *Kitchen) getChatMember(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	userID, err := p.chat("user_id")
	if err != nil {
		return nil, err
	}

	member, found := k.world.standingOf(chatID, userID)
	if !found {
		return nil, requestError("user not found")
	}
	// By pointer: only then does the library encode the status.
	return &member, nil
}

func (k *Kitchen) getChatAdministrators(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	if _, found := k.world.info(chatID); !found {
		return nil, requestError("chat not found")
	}
	return k.world.administrators(chatID), nil
}

func (k *Kitchen) getChatMemberCount(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	if _, found := k.world.info(chatID); !found {
		return nil, requestError("chat not found")
	}
	// The bot counts too.
	return len(k.world.roster(chatID)) + 1, nil
}

// The calls that change somebody else's standing. Each one is the bot's own
// doing, so nothing comes back to it: the kitchen never delivers a bot its own
// actions, here any more than for a message it sends.
func (k *Kitchen) banChatMember(p params) (any, error) {
	return k.manage(p, RestrictMembers, "restrict a chat member", func(s *standing) {
		s.status, s.silenced = models.ChatMemberTypeBanned, true
	})
}

func (k *Kitchen) unbanChatMember(p params) (any, error) {
	// Unbanning lets them back in; it does not put them back.
	return k.manage(p, RestrictMembers, "restrict a chat member", func(s *standing) {
		s.status, s.silenced = models.ChatMemberTypeLeft, false
	})
}

func (k *Kitchen) restrictChatMember(p params) (any, error) {
	var allowed models.ChatPermissions
	if err := p.decode("permissions", &allowed); err != nil {
		return nil, badRequest("permissions")
	}
	return k.manage(p, RestrictMembers, "restrict a chat member", func(s *standing) {
		s.status, s.silenced = models.ChatMemberTypeRestricted, !allowed.CanSendMessages
	})
}

// Promoting with nothing granted is how Telegram spells a demotion.
func (k *Kitchen) promoteChatMember(p params) (any, error) {
	granted := rightsIn(p)
	return k.manage(p, PromoteMembers, "promote a chat member", func(s *standing) {
		s.rights, s.silenced = granted, false
		if s.status = models.ChatMemberTypeAdministrator; len(granted) == 0 {
			s.status = models.ChatMemberTypeMember
		}
	})
}

func (k *Kitchen) manage(p params, need Right, what string, apply func(*standing)) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	userID, err := p.chat("user_id")
	if err != nil {
		return nil, err
	}
	if err := k.world.manage(chatID, userID, need, what, apply); err != nil {
		return nil, err
	}
	return true, nil
}

func (k *Kitchen) pinChatMessage(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	messageID, err := p.messageID()
	if err != nil {
		return nil, err
	}

	pinned, err := k.world.pin(chatID, messageID)
	if err != nil {
		return nil, err
	}
	k.world.add(chatID, models.Message{PinnedMessage: &models.MaybeInaccessibleMessage{
		Type: models.MaybeInaccessibleMessageTypeMessage, Message: &pinned,
	}})
	return true, nil
}

func (k *Kitchen) unpinChatMessage(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	// The message id is optional here: without one the newest pin comes back.
	if err := k.world.unpin(chatID, p.number("message_id"), false); err != nil {
		return nil, err
	}
	return true, nil
}

func (k *Kitchen) unpinAllChatMessages(p params) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	if err := k.world.unpin(chatID, 0, true); err != nil {
		return nil, err
	}
	return true, nil
}
