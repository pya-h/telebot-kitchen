package kitchen

import (
	"time"

	"github.com/go-telegram/bot/models"
)

// Button is one inline keyboard button as the user sees it.
type Button struct {
	Label string
	Data  string
	URL   string
}

// Message is a chat entry as a test reads it, either side's.
type Message struct {
	ID       int
	Text     string
	FromBot  bool
	Sent     time.Time
	Keyboard [][]Button
}

// Screen is what the user has at the top of their chat right now.
type Screen struct {
	Message
}

func (m Message) HasButton(labelOrData string) bool {
	_, ok := m.Button(labelOrData)
	return ok
}

func (m Message) Button(labelOrData string) (Button, bool) {
	return findButton(m.Keyboard, labelOrData)
}

// Buttons flattens the keyboard into reading order.
func (m Message) Buttons() []Button {
	var buttons []Button
	for _, row := range m.Keyboard {
		buttons = append(buttons, row...)
	}
	return buttons
}

func (m Message) subject() subject {
	return subject{text: m.Text, keyboard: m.Keyboard}
}

// Screen is the newest message in the user's chat, which may be their own.
func (u *User) Screen() Screen {
	m, ok := u.kitchen.world.latest(u.chatID)
	if !ok {
		return Screen{}
	}
	return Screen{Message: u.kitchen.view(m)}
}

func (u *User) History() []Message { return u.kitchen.History(u.chatID) }

func (k *Kitchen) History(chatID int64) []Message {
	log := k.world.history(chatID)
	view := make([]Message, len(log))
	for i, m := range log {
		view[i] = k.view(m)
	}
	return view
}

func (k *Kitchen) view(m models.Message) Message {
	// Telegram never sets both, so a caption reads as the message's text.
	text := m.Text
	if text == "" {
		text = m.Caption
	}

	return Message{
		ID:       m.ID,
		Text:     text,
		FromBot:  m.From != nil && m.From.ID == k.botUser().ID,
		Sent:     time.Unix(int64(m.Date), 0).UTC(),
		Keyboard: buttonsOf(m.ReplyMarkup),
	}
}
