package kitchen

import (
	"slices"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
)

// How long the bot may write to somebody whose join request it has not answered.
const knockWindow = 5 * time.Minute

type joinRequest struct {
	who models.User
	at  time.Time
}

// joinBook holds the requests waiting on an answer. A request is not a
// standing: somebody asking to join is not in the chat until they are let in.
type joinBook struct {
	mu     sync.Mutex
	asking map[int64]map[int64]joinRequest
}

func newJoinBook() *joinBook { return &joinBook{asking: map[int64]map[int64]joinRequest{}} }

func (b *joinBook) ask(chatID int64, who models.User, at time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	waiting, ok := b.asking[chatID]
	if !ok {
		waiting = map[int64]joinRequest{}
		b.asking[chatID] = waiting
	}
	if _, already := waiting[who.ID]; already {
		return false
	}
	waiting[who.ID] = joinRequest{who: who, at: at}
	return true
}

// answer takes the request off the list, and says whether there was one.
func (b *joinBook) answer(chatID, userID int64) (models.User, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	asked, waiting := b.asking[chatID][userID]
	if waiting {
		delete(b.asking[chatID], userID)
	}
	return asked.who, waiting
}

func (b *joinBook) knocking(userID int64, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, waiting := range b.asking {
		if asked, ok := waiting[userID]; ok && now.Before(asked.at.Add(knockWindow)) {
			return true
		}
	}
	return false
}

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
		s.absent = !s.present()
		s.status, s.silenced = models.ChatMemberTypeRestricted, !allowed.CanSendMessages
	})
}

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

func (k *Kitchen) approveChatJoinRequest(p params) (any, error) {
	return k.answerJoinRequest(p, true)
}

func (k *Kitchen) declineChatJoinRequest(p params) (any, error) {
	return k.answerJoinRequest(p, false)
}

func (k *Kitchen) answerJoinRequest(p params, admit bool) (any, error) {
	chatID, err := p.chatID()
	if err != nil {
		return nil, err
	}
	userID, err := p.chat("user_id")
	if err != nil {
		return nil, err
	}
	if err := k.world.mayManage(chatID, InviteUsers, "manage chat join requests"); err != nil {
		return nil, err
	}

	who, waiting := k.joins.answer(chatID, userID)
	if !waiting {
		return nil, requestError("HIDE_REQUESTER_MISSING")
	}
	if !admit {
		return true, nil
	}

	sender := k.botUser()
	was, now, chat := k.world.restand(chatID, who, standing{status: models.ChatMemberTypeMember})
	k.deliver(models.Update{ChatMember: &models.ChatMemberUpdated{
		Chat: chat, From: sender, Date: int(k.clock.Now().Unix()),
		OldChatMember: was, NewChatMember: now,
	}})
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
