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

// User is a virtual person talking to the bot through the kitchen.
type User struct {
	kitchen  *Kitchen
	id       int64
	info     Identity
	chatID   int64
	awaiting int
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
			chatID:  id,
		}
		k.users[id] = u
	}
	for _, opt := range opts {
		opt(&u.info)
	}
	k.world.join(u.telegram())
	return u
}

func (u *User) ID() int64 { return u.id }

func (u *User) ChatID() int64 { return u.chatID }

func (u *User) Send(text string) {
	u.say(models.Message{Text: text, Entities: commandEntities(text)})
}

func (u *User) SendCommand(name string, args ...string) {
	text := "/" + strings.TrimPrefix(name, "/")
	if len(args) > 0 {
		text += " " + strings.Join(args, " ")
	}
	u.Send(text)
}

// Tap presses an inline button by its visible label or its callback data.
func (u *User) Tap(labelOrData string) {
	screens := u.kitchen.world.keyboards(u.chatID, u.kitchen.reach())
	if len(screens) == 0 {
		u.kitchen.tb.Errorf("kitchen: user %d has no buttons on screen, so %q cannot be tapped", u.ID(), labelOrData)
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
			u.kitchen.tb.Errorf("kitchen: button %q sends no callback data, so tapping it does not reach the bot", labelOrData)
			return
		}
		u.press(screen, button)
		return
	}

	u.kitchen.tb.Errorf("kitchen: user %d has no button %q on screen, found: %s", u.ID(), labelOrData, buttonLabels(reachable))
}

func (u *User) press(screen models.Message, button Button) {
	u.awaitFromNow()

	sender := u.identity()
	u.kitchen.deliver(models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   u.kitchen.world.nextQuery(),
		From: sender,
		Message: models.MaybeInaccessibleMessage{
			Type:    models.MaybeInaccessibleMessageTypeMessage,
			Message: &screen,
		},
		ChatInstance: fmt.Sprintf("chat-%d", u.chatID),
		Data:         button.Data,
	}})
}

func (u *User) SendPhoto(name string, data []byte, caption string) {
	f := u.kitchen.files.add(name, data)
	u.say(models.Message{Photo: u.kitchen.files.photoSizes(f.ID), Caption: caption})
}

func (u *User) ShareLocation(latitude, longitude float64) {
	u.say(models.Message{Location: &models.Location{Latitude: latitude, Longitude: longitude}})
}

func (u *User) say(m models.Message) {
	sender := u.identity()
	m.From = &sender

	sent := u.kitchen.world.add(u.chatID, m)
	u.awaiting = sent.ID
	u.kitchen.deliver(models.Update{Message: &sent})
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
