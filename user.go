package kitchen

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/go-telegram/bot/models"
)

type UserOption func(*userSetup)

type userSetup struct {
	Identity
	started bool
}

type Identity struct {
	Username     string
	FirstName    string
	LastName     string
	LanguageCode string
}

func WithUsername(username string) UserOption {
	return func(s *userSetup) { s.Username = username }
}

func WithFullName(first, last string) UserOption {
	return func(s *userSetup) { s.FirstName, s.LastName = first, last }
}

func WithLanguage(code string) UserOption {
	return func(s *userSetup) { s.LanguageCode = code }
}

// Started is a user who opened the bot's private chat before the test began.
func Started() UserOption {
	return func(s *userSetup) { s.started = true }
}

type User struct {
	*Member

	kitchen *Kitchen
	id      int64
	info    Identity
	shared  map[int64]*Member
}

// User returns the virtual user with this id, creating them on first mention.
func (k *Kitchen) User(id int64, opts ...UserOption) *User {
	// Bots branch on the sign, so a negative id would test a person who cannot exist.
	if id <= 0 {
		k.tb.Errorf("kitchen: a user id must be positive, as Telegram's are, not %d", id)
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	u, ok := k.users[id]
	if !ok {
		u = &User{
			kitchen: k,
			id:      id,
			info:    Identity{FirstName: fmt.Sprintf("User%d", id)},
			shared:  map[int64]*Member{},
		}
		u.Member = &Member{user: u, chat: &Chat{kitchen: k, id: id, kind: models.ChatTypePrivate}}
		k.users[id] = u
	}
	setup := userSetup{Identity: u.info}
	for _, opt := range opts {
		opt(&setup)
	}
	u.info = setup.Identity
	k.world.join(id, u.telegram())
	if setup.started {
		k.world.start(id)
	}
	return u
}

// knownUser is anyone the test has introduced, whether or not they have been
// anywhere yet.
func (k *Kitchen) knownUser(id int64) (models.User, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	u, ok := k.users[id]
	if !ok {
		return models.User{}, false
	}
	return u.telegram(), true
}

// In returns this user inside a shared chat, with its own place in the
// conversation. Speaking puts them on the roster; Join announces them.
func (u *User) In(c *Chat) *Member {
	u.kitchen.mu.Lock()
	defer u.kitchen.mu.Unlock()

	m, ok := u.shared[c.id]
	if !ok {
		m = &Member{user: u, chat: c}
		u.shared[c.id] = m
	}
	return m
}

func (u *User) ID() int64 { return u.id }

func (u *User) BlockBot() { u.blocking(models.ChatMemberTypeBanned) }

func (u *User) UnblockBot() { u.blocking(models.ChatMemberTypeMember) }

func (u *User) blocking(to models.ChatMemberType) {
	k := u.kitchen
	was, now, chat := k.world.restandBot(u.id, standing{status: to})
	if was.Type == now.Type {
		if to == models.ChatMemberTypeBanned {
			k.tb.Errorf("kitchen: %s has already blocked the bot", u)
		} else {
			k.tb.Errorf("kitchen: %s has not blocked the bot, so there is nothing to unblock", u)
		}
		return
	}
	u.awaitFromNow()
	k.deliver(models.Update{MyChatMember: u.changed(chat, was, now)})
}

func (u *User) identity() models.User {
	u.kitchen.mu.RLock()
	defer u.kitchen.mu.RUnlock()
	return u.telegram()
}

// telegram is the user as Telegram would carry them; the caller holds the lock.
func (u *User) telegram() models.User {
	return models.User{
		ID:           u.id,
		FirstName:    u.info.FirstName,
		LastName:     u.info.LastName,
		Username:     u.info.Username,
		LanguageCode: u.info.LanguageCode,
	}
}

func commandEntities(text string) []models.MessageEntity {
	if !strings.HasPrefix(text, "/") {
		return nil
	}
	command := text
	if end := strings.IndexFunc(text, unicode.IsSpace); end > 0 {
		command = text[:end]
	}
	if len(command) == 1 {
		return nil
	}
	return []models.MessageEntity{{
		Type:   models.MessageEntityTypeBotCommand,
		Length: utf16Len(command),
	}}
}

// Telegram measures entity offsets and lengths in UTF-16 code units, so any
// text past the basic plane counts double.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}
