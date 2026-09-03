package kitchen

import (
	"net/http"
	"slices"

	"github.com/go-telegram/bot/models"
)

// Right is one thing an administrator may do. Only the ones a call can be
// refused for are enforced; the rest are reported and nothing more.
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

type standing struct {
	user     models.User
	status   models.ChatMemberType
	rights   []Right
	silenced bool
}

func (s standing) may(r Right) bool {
	if s.status != models.ChatMemberTypeAdministrator {
		return s.status == models.ChatMemberTypeOwner
	}
	return slices.Contains(s.rights, r)
}

func (s standing) present() bool {
	return s.status != models.ChatMemberTypeLeft && s.status != models.ChatMemberTypeBanned
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
			User: &s.user, IsMember: true, CanSendMessages: !s.silenced,
		}}

	case models.ChatMemberTypeLeft:
		return models.ChatMember{Type: s.status, Left: &models.ChatMemberLeft{User: &s.user}}

	case models.ChatMemberTypeBanned:
		return models.ChatMember{Type: s.status, Banned: &models.ChatMemberBanned{User: &s.user}}

	default:
		return models.ChatMember{Type: models.ChatMemberTypeMember, Member: &models.ChatMemberMember{User: &s.user}}
	}
}

// mayPost reports why the bot cannot put a message in this chat, if it cannot.
func (w *world) mayPost(chatID int64) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	c, ok := w.chats[chatID]
	if !ok {
		return nil // the private chat this call opens
	}
	switch {
	case c.bot.status == models.ChatMemberTypeBanned:
		return forbidden("bot was kicked from the " + string(c.info.Type) + " chat")
	case !c.bot.present():
		return forbidden("bot is not a member of the " + string(c.info.Type) + " chat")
	case c.info.Type == models.ChatTypeChannel && !c.bot.may(PostMessages):
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

func (c *chat) mayEdit(m *models.Message, botID int64) error {
	if m.From != nil && m.From.ID == botID {
		return nil
	}
	if c.info.Type == models.ChatTypeChannel && c.bot.may(EditMessages) {
		return nil
	}
	return requestError("message can't be edited")
}

func (c *chat) mayPin() error {
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
