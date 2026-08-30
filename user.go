package kitchen

import (
	"fmt"
	"strings"

	"github.com/go-telegram/bot/models"
)

type UserOption func(*models.User)

func WithUsername(username string) UserOption {
	return func(u *models.User) { u.Username = username }
}

func WithFullName(first, last string) UserOption {
	return func(u *models.User) { u.FirstName, u.LastName = first, last }
}

func WithLanguage(code string) UserOption {
	return func(u *models.User) { u.LanguageCode = code }
}

// User is a virtual person talking to the bot through the kitchen.
type User struct {
	kitchen *Kitchen
	info    models.User
	chatID  int64
}

// User returns the virtual user with this id, creating them on first mention.
func (k *Kitchen) User(id int64, opts ...UserOption) *User {
	k.mu.Lock()
	defer k.mu.Unlock()

	u, ok := k.users[id]
	if !ok {
		u = &User{
			kitchen: k,
			info:    models.User{ID: id, FirstName: fmt.Sprintf("User%d", id)},
			chatID:  id,
		}
		k.users[id] = u
	}
	for _, opt := range opts {
		opt(&u.info)
	}
	k.world.join(u.info)
	return u
}

func (u *User) ID() int64 { return u.info.ID }

func (u *User) ChatID() int64 { return u.chatID }

func (u *User) Send(text string) {
	u.say(models.Message{Text: text})
}

func (u *User) SendCommand(name string, args ...string) {
	command := "/" + strings.TrimPrefix(name, "/")
	text := command
	if len(args) > 0 {
		text += " " + strings.Join(args, " ")
	}

	u.say(models.Message{
		Text: text,
		Entities: []models.MessageEntity{{
			Type:   models.MessageEntityTypeBotCommand,
			Length: utf16Len(command),
		}},
	})
}

func (u *User) say(m models.Message) {
	sender := u.identity()
	m.From = &sender

	sent := u.kitchen.world.add(u.chatID, m)
	u.kitchen.deliver(models.Update{Message: &sent})
}

func (u *User) identity() models.User {
	u.kitchen.mu.RLock()
	defer u.kitchen.mu.RUnlock()
	return u.info
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
