package kitchen

import (
	"errors"
	"net/http"
	"slices"

	"github.com/go-telegram/bot/models"
)

type Right string

const (
	PostMessages    Right = "can_post_messages"
	EditMessages    Right = "can_edit_messages"
	DeleteMessages  Right = "can_delete_messages"
	PinMessages     Right = "can_pin_messages"
	RestrictMembers Right = "can_restrict_members"
	PromoteMembers  Right = "can_promote_members"
	InviteUsers     Right = "can_invite_users"
	ChangeInfo      Right = "can_change_info"
)

var everyRight = []Right{
	PostMessages, EditMessages, DeleteMessages, PinMessages,
	RestrictMembers, PromoteMembers, InviteUsers, ChangeInfo,
}

// The rest of what a promotion may grant. Nothing is refused for them, but a
// promotion naming only these is still a promotion.
var reportedRights = []string{
	"is_anonymous", "can_manage_chat", "can_manage_video_chats", "can_manage_topics",
	"can_post_stories", "can_edit_stories", "can_delete_stories",
}

type standing struct {
	user     models.User
	status   models.ChatMemberType
	rights   []Right
	silenced bool
	absent   bool // restricted from outside the chat, which Telegram keeps apart
}

func (s standing) may(r Right) bool {
	if s.status != models.ChatMemberTypeAdministrator {
		return s.status == models.ChatMemberTypeOwner
	}
	return slices.Contains(s.rights, r)
}

func (s standing) present() bool {
	switch s.status {
	case models.ChatMemberTypeLeft, models.ChatMemberTypeBanned:
		return false
	case models.ChatMemberTypeRestricted:
		return !s.absent
	}
	return true
}

// admitted reads presence off what getChatMember answers, for callers holding
// that rather than the standing behind it.
func admitted(member models.ChatMember) bool {
	switch member.Type {
	case models.ChatMemberTypeLeft, models.ChatMemberTypeBanned:
		return false
	case models.ChatMemberTypeRestricted:
		return member.Restricted.IsMember
	}
	return true
}

// rightsIn reads a promotion, whose parameters are named after the rights.
func rightsIn(p params) []Right {
	var granted []Right
	for _, r := range everyRight {
		if p.flag(string(r)) {
			granted = append(granted, r)
		}
	}
	return granted
}

func (s standing) chatMember() models.ChatMember {
	switch s.status {
	case models.ChatMemberTypeOwner:
		return models.ChatMember{Type: s.status, Owner: &models.ChatMemberOwner{User: &s.user}}

	case models.ChatMemberTypeAdministrator:
		return models.ChatMember{Type: s.status, Administrator: &models.ChatMemberAdministrator{
			User:               s.user,
			CanBeEdited:        true,
			CanManageChat:      true,
			CanPostMessages:    s.may(PostMessages),
			CanEditMessages:    s.may(EditMessages),
			CanDeleteMessages:  s.may(DeleteMessages),
			CanPinMessages:     s.may(PinMessages),
			CanRestrictMembers: s.may(RestrictMembers),
			CanPromoteMembers:  s.may(PromoteMembers),
			CanInviteUsers:     s.may(InviteUsers),
			CanChangeInfo:      s.may(ChangeInfo),
		}}

	case models.ChatMemberTypeRestricted:
		return models.ChatMember{Type: s.status, Restricted: &models.ChatMemberRestricted{
			User: &s.user, IsMember: !s.absent, CanSendMessages: !s.silenced,
		}}

	case models.ChatMemberTypeLeft:
		return models.ChatMember{Type: s.status, Left: &models.ChatMemberLeft{User: &s.user}}

	case models.ChatMemberTypeBanned:
		return models.ChatMember{Type: s.status, Banned: &models.ChatMemberBanned{User: &s.user}}

	default:
		return models.ChatMember{Type: models.ChatMemberTypeMember, Member: &models.ChatMemberMember{User: &s.user}}
	}
}

var (
	errNotStarted = forbidden("bot can't initiate conversation with a user")
	errBlocked    = forbidden("bot was blocked by the user")
)

// A user waiting on a join request may be written to for a while, started or not.
func (k *Kitchen) mayPost(chatID int64) error {
	err := k.world.mayPost(chatID)
	if errors.Is(err, errNotStarted) && k.joins.knocking(chatID, k.clock.Now()) {
		return nil
	}
	return err
}

// reach is why the bot cannot act in this chat at all, ahead of any right it
// may or may not hold there.
func (c *chat) reach() error {
	switch {
	case c.blocked():
		return errBlocked
	case c.info.Type == models.ChatTypePrivate && !c.started:
		return errNotStarted
	case c.bot.status == models.ChatMemberTypeBanned:
		return forbidden("bot was kicked from the " + string(c.info.Type) + " chat")
	case !c.bot.present():
		return forbidden("bot is not a member of the " + string(c.info.Type) + " chat")
	}
	return nil
}

func (w *world) reach(chatID int64) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return requestError("chat not found")
	}
	return c.reach()
}

// mayPost reports why the bot cannot put a message in this chat, if it cannot.
func (w *world) mayPost(chatID int64) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return requestError("chat not found")
	}
	if err := c.reach(); err != nil {
		return err
	}
	if c.info.Type == models.ChatTypeChannel && !c.bot.may(PostMessages) {
		return requestError("need administrator rights in the channel chat")
	}
	return nil
}

// The may* checks below run with the world lock held.
func (c *chat) mayDelete(m *models.Message, botID int64) error {
	if m.From != nil && m.From.ID == botID {
		return nil
	}
	if c.info.Type == models.ChatTypePrivate || c.bot.may(DeleteMessages) {
		return nil
	}
	return requestError("message can't be deleted for everyone")
}

func (c *chat) blocked() bool {
	return c.info.Type == models.ChatTypePrivate && c.bot.status == models.ChatMemberTypeBanned
}

func (c *chat) mayEdit(m *models.Message, botID int64) error {
	if c.blocked() {
		return errBlocked
	}
	if m.From != nil && m.From.ID == botID {
		return nil
	}
	if m.ViaBot != nil && m.ViaBot.ID == botID {
		return nil
	}
	if c.info.Type == models.ChatTypeChannel && c.bot.may(EditMessages) {
		return nil
	}
	return requestError("message can't be edited")
}

func (c *chat) mayPin() error {
	if err := c.reach(); err != nil {
		return err
	}
	if c.info.Type == models.ChatTypePrivate || c.bot.may(PinMessages) {
		return nil
	}
	return requestError("not enough rights to pin a message")
}

// mayManage covers the calls that change somebody else's standing, which a
// private chat has none of.
func (c *chat) mayManage(need Right, what string) error {
	if c.info.Type == models.ChatTypePrivate {
		return requestError("method is available for supergroup and channel chats only")
	}
	if !c.bot.may(need) {
		return requestError("not enough rights to " + what)
	}
	return nil
}

func forbidden(description string) *apiError {
	return &apiError{Code: http.StatusForbidden, Description: "Forbidden: " + description}
}
