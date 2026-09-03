package kitchen

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/go-telegram/bot/models"
)

type UserOption func(*Identity)

// Identity is who a virtual person is to Telegram. Their id is not on it: it is
// the key they are registered under, and an option must not be able to move it.
type Identity struct {
	Username     string
	FirstName    string
	LastName     string
	LanguageCode string
}

func WithUsername(username string) UserOption {
	return func(i *Identity) { i.Username = username }
}

func WithFullName(first, last string) UserOption {
	return func(i *Identity) { i.FirstName, i.LastName = first, last }
}

func WithLanguage(code string) UserOption {
	return func(i *Identity) { i.LanguageCode = code }
}

// User is a virtual person talking to the bot through the kitchen. The embedded
// member is their private chat, so the plain verbs still mean what they did.
type User struct {
	*Member

	kitchen *Kitchen
	id      int64
	info    Identity
	shared  map[int64]*Member
}

// User returns the virtual user with this id, creating them on first mention.
func (k *Kitchen) User(id int64, opts ...UserOption) *User {
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
	for _, opt := range opts {
		opt(&u.info)
	}
	k.world.join(id, u.telegram())
	return u
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

// Telegram parses entities itself, so a message opening with a command carries
// one whether the user typed the text or asked for the command by name.
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
