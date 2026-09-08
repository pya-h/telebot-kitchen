package kitchen

import (
	"slices"

	"github.com/go-telegram/bot/models"
)

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

	if _, found := k.world.info(chatID); !found {
		return nil, requestError("chat not found")
	}
	if member, found := k.world.standingOf(chatID, userID); found {
		return &member, nil
	}

	// Never having joined is a standing of its own, not a missing record. A gate
	// reading an error where Telegram sends "left" would fail open.
	who, known := k.knownUser(userID)
	if !known {
		return nil, requestError("user not found")
	}
	member := standing{user: who, status: models.ChatMemberTypeLeft}.chatMember()
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
	count := len(k.world.roster(chatID))
	if k.world.botPresent(chatID) {
		count++
	}
	return count, nil
}

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
		// Restricting somebody who is not in the chat waits for them rather than
		// putting them back, so read presence before the status is overwritten.
		s.absent = !s.present()
		s.status, s.silenced = models.ChatMemberTypeRestricted, !allowed.CanSendMessages
	})
}

// Promoting with nothing granted is how Telegram spells a demotion, so the
// rights the kitchen only reports still have to count towards the status.
func (k *Kitchen) promoteChatMember(p params) (any, error) {
	granted := rightsIn(p)
	demoted := len(granted) == 0 && !slices.ContainsFunc(reportedRights, p.flag)
	return k.manage(p, PromoteMembers, "promote a chat member", func(s *standing) {
		s.rights, s.silenced = granted, false
		if s.status = models.ChatMemberTypeAdministrator; demoted {
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
	// No sender: what the bot does is not a reply coming back to a member.
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
