package kitchen

import (
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
)

type Button struct {
	Label string
	Data  string
	URL   string
}

type Message struct {
	ID            int
	ChatID        int64
	Text          string
	From          string
	FromBot       bool
	ForwardedFrom string
	Media         string
	Event         string // "joined", "left", "pinned" or "moved"
	Sent          time.Time
	Keyboard      [][]Button
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
	return subject{chatID: m.ChatID, text: m.Text, keyboard: m.Keyboard}
}

// Screen returns the newest message in the chat, which may be the member's own.
func (m *Member) Screen() Screen {
	newest, ok := m.kitchen().world.latest(m.chat.id)
	if !ok {
		return Screen{}
	}
	return Screen{Message: m.kitchen().view(newest)}
}

func (m *Member) History() []Message { return m.chat.History() }

func (k *Kitchen) History(chatID int64) []Message {
	log := k.world.history(chatID)
	view := make([]Message, len(log))
	for i, m := range log {
		view[i] = k.view(m)
	}
	return view
}

func (k *Kitchen) view(m models.Message) Message {
	text := m.Text
	if text == "" {
		text = m.Caption
	}

	media := ""
	switch {
	case len(m.Photo) > 0:
		media = "photo"
	case m.Location != nil:
		media = "location"
	}

	event := ""
	switch {
	case len(m.NewChatMembers) > 0:
		event = "joined"
	case m.LeftChatMember != nil:
		event = "left"
	case m.PinnedMessage != nil:
		event = "pinned"
	case m.MigrateToChatID != 0 || m.MigrateFromChatID != 0:
		event = "moved"
	}

	return Message{
		ID:            m.ID,
		ChatID:        m.Chat.ID,
		Text:          text,
		From:          author(m),
		FromBot:       m.From != nil && m.From.ID == k.botUser().ID,
		ForwardedFrom: forwardedFrom(m.ForwardOrigin),
		Media:         media,
		Event:         event,
		Sent:          time.Unix(int64(m.Date), 0).UTC(),
		Keyboard:      buttonsOf(m.ReplyMarkup),
	}
}

// author is who a client shows above the message: a channel signs its posts
// with its own name rather than with a person's.
func author(m models.Message) string {
	if m.From != nil {
		return displayName(m.From)
	}
	if m.SenderChat != nil {
		return m.SenderChat.Title
	}
	return ""
}

func forwardedFrom(o *models.MessageOrigin) string {
	if o == nil {
		return ""
	}
	switch {
	case o.MessageOriginUser != nil:
		return displayName(&o.MessageOriginUser.SenderUser)
	case o.MessageOriginChannel != nil:
		return o.MessageOriginChannel.Chat.Title
	}
	return ""
}

func displayName(u *models.User) string {
	if u == nil {
		return ""
	}
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}
