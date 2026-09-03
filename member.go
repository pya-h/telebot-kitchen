package kitchen

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-telegram/bot/models"
)

var errNotTheirs = errors.New("kitchen: not the member's own message")

// Member is a user inside one chat, with their own place in what was said there.
type Member struct {
	user *User
	chat *Chat

	awaiting int
}

func (m *Member) ChatID() int64 { return m.chat.id }

func (m *Member) kitchen() *Kitchen { return m.chat.kitchen }

func (m *Member) Send(text string) {
	m.say(models.Message{Text: text, Entities: commandEntities(text)})
}

func (m *Member) SendCommand(name string, args ...string) {
	text := "/" + strings.TrimPrefix(name, "/")
	if len(args) > 0 {
		text += " " + strings.Join(args, " ")
	}
	m.Send(text)
}

// Tap presses an inline button by its visible label or its callback data.
func (m *Member) Tap(labelOrData string) {
	k := m.kitchen()
	screens := k.world.keyboards(m.chat.id, k.reach())
	if len(screens) == 0 {
		k.tb.Errorf("kitchen: %s has no buttons on screen, so %q cannot be tapped", m, labelOrData)
		return
	}

	var reachable [][]Button
	for _, screen := range screens {
		rows := buttonsOf(screen.ReplyMarkup)
		button, ok := findButton(rows, labelOrData)
		if !ok {
			reachable = append(reachable, rows...)
			continue
		}
		if button.Data == "" {
			k.tb.Errorf("kitchen: button %q sends no callback data, so tapping it does not reach the bot", labelOrData)
			return
		}
		m.press(screen, button)
		return
	}

	k.tb.Errorf("kitchen: %s has no button %q on screen, found: %s", m, labelOrData, buttonLabels(reachable))
}

func (m *Member) press(screen models.Message, button Button) {
	m.awaitFromNow()

	sender := m.user.identity()
	m.kitchen().deliver(models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   m.kitchen().world.nextQuery(),
		From: sender,
		Message: models.MaybeInaccessibleMessage{
			Type:    models.MaybeInaccessibleMessageTypeMessage,
			Message: &screen,
		},
		ChatInstance: fmt.Sprintf("chat-%d", m.chat.id),
		Data:         button.Data,
	}})
}

func (m *Member) SendPhoto(name string, data []byte, caption string) {
	f := m.kitchen().files.add(name, data)
	m.say(models.Message{Photo: m.kitchen().files.photoSizes(f.ID), Caption: caption})
}

func (m *Member) ShareLocation(latitude, longitude float64) {
	m.say(models.Message{Location: &models.Location{Latitude: latitude, Longitude: longitude}})
}

func (m *Member) say(msg models.Message) {
	if m.chat.kind == models.ChatTypeChannel {
		m.kitchen().tb.Errorf("kitchen: %s cannot speak, a channel carries posts rather than what its subscribers say", m)
		return
	}

	sender := m.user.identity()
	msg.From = &sender

	// Speaking somewhere puts you there; Join is what announces it.
	if !m.kitchen().world.speaking(m.chat.id, sender) {
		m.kitchen().tb.Errorf("kitchen: the bot restricted %s, so nothing they say arrives", m)
		return
	}
	sent := m.kitchen().world.add(m.chat.id, msg)
	m.awaiting = sent.ID
	m.kitchen().deliver(models.Update{Message: &sent})
}

// String names the member for a failure; the chat only when it is not their own.
func (m *Member) String() string {
	if m.chat.kind == models.ChatTypePrivate {
		return fmt.Sprintf("user %d", m.user.id)
	}
	return fmt.Sprintf("user %d in %q", m.user.id, m.chat.Title())
}

// Edit is the member rewording what they said. What the bot edits through the
// API is its own doing, and comes back to nobody.
func (m *Member) Edit(sent Message, text string) Message {
	edited, found, err := m.kitchen().world.edit(m.chat.id, sent.ID, func(_ *chat, msg *models.Message) error {
		if msg.From == nil || msg.From.ID != m.user.id {
			return errNotTheirs
		}
		msg.Text, msg.Entities = text, commandEntities(text)
		return nil
	})
	switch {
	case !found:
		m.kitchen().tb.Errorf("kitchen: %s has no message %d to edit", m, sent.ID)
		return Message{}
	case err != nil:
		m.kitchen().tb.Errorf("kitchen: message %d is not %s's to edit", sent.ID, m)
		return Message{}
	}

	m.kitchen().deliver(models.Update{EditedMessage: &edited})
	return m.kitchen().view(edited)
}

// Join announces the arrival that In does not.
func (m *Member) Join() {
	who := m.user.identity()
	m.announce(who, standing{status: models.ChatMemberTypeMember}, &models.Message{NewChatMembers: []models.User{who}})
}

func (m *Member) Leave() {
	who := m.user.identity()
	m.announce(who, standing{status: models.ChatMemberTypeLeft}, &models.Message{LeftChatMember: &who})
}

// Promote makes somebody an administrator, with every right when none are named.
func (m *Member) Promote(u *User, rights ...Right) {
	m.announce(u.identity(), standing{status: models.ChatMemberTypeAdministrator, rights: granted(rights)}, nil)
}

func (m *Member) Demote(u *User) {
	m.announce(u.identity(), standing{status: models.ChatMemberTypeMember}, nil)
}

// The bot's own standing, which reaches it as my_chat_member.
func (m *Member) PromoteBot(rights ...Right) {
	m.announceBot(standing{status: models.ChatMemberTypeAdministrator, rights: granted(rights)})
}

func (m *Member) DemoteBot() {
	m.announceBot(standing{status: models.ChatMemberTypeMember})
}

func (m *Member) RemoveBot() {
	m.announceBot(standing{status: models.ChatMemberTypeBanned})
}

// announce records the new standing and tells the bot, service message first.
func (m *Member) announce(who models.User, to standing, service *models.Message) {
	if !m.roster() {
		return
	}
	was, now, chat := m.kitchen().world.restand(m.chat.id, who, to)

	if service != nil {
		service.From = &who
		sent := m.kitchen().world.add(m.chat.id, *service)
		m.awaiting = sent.ID
		m.kitchen().deliver(models.Update{Message: &sent})
	}
	m.kitchen().deliver(models.Update{ChatMember: m.changed(chat, was, now)})
}

func (m *Member) announceBot(to standing) {
	if !m.roster() {
		return
	}
	was, now, chat := m.kitchen().world.restandBot(m.chat.id, to)
	m.kitchen().deliver(models.Update{MyChatMember: m.changed(chat, was, now)})
}

func (m *Member) changed(chat models.Chat, was, now models.ChatMember) *models.ChatMemberUpdated {
	return &models.ChatMemberUpdated{
		Chat:          chat,
		From:          m.user.identity(),
		Date:          int(m.kitchen().clock.Now().Unix()),
		OldChatMember: was,
		NewChatMember: now,
	}
}

func (m *Member) roster() bool {
	if m.chat.kind == models.ChatTypePrivate {
		m.kitchen().tb.Errorf("kitchen: %s is in a private chat, which has nobody to admit or remove", m)
		return false
	}
	return true
}

func granted(rights []Right) []Right {
	if len(rights) == 0 {
		return everyRight
	}
	return slices.Clone(rights)
}
