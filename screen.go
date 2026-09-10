package kitchen

import (
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
)

type Button struct {
	Label string `json:"label"`
	Data  string `json:"data,omitempty"`
	URL   string `json:"url,omitempty"`
}

type Message struct {
	ID            int        `json:"id"`
	ChatID        int64      `json:"chat_id"`
	Text          string     `json:"text,omitempty"`
	From          string     `json:"from,omitempty"`
	FromBot       bool       `json:"from_bot,omitempty"`
	ForwardedFrom string     `json:"forwarded_from,omitempty"`
	Media         string     `json:"media,omitempty"`
	FileID        string     `json:"file_id,omitempty"`
	Album         string     `json:"album,omitempty"`
	Options       []string   `json:"options,omitempty"` // what a poll asks, in the order it asks it
	Reactions     []string   `json:"reactions,omitempty"`
	Entities      []Entity   `json:"entities,omitempty"`
	Event         string     `json:"event,omitempty"` // "joined", "left", "pinned", "moved", "invoice", "paid" or "refunded"
	Sent          time.Time  `json:"sent"`
	Keyboard      [][]Button `json:"keyboard,omitempty"`

	rich    string
	carries string
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
	text, entities := m.Text, m.Entities
	if text == "" {
		text, entities = m.Caption, m.CaptionEntities
	}

	media, _ := mediaOf(&m)

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
	case m.Invoice != nil:
		event = "invoice"
	case m.SuccessfulPayment != nil:
		event = "paid"
	case m.RefundedPayment != nil:
		event = "refunded"
	}
	// These carry no text of their own; a client shows each by what it is.
	if text == "" {
		switch {
		case m.Invoice != nil:
			text = m.Invoice.Title
		case m.Venue != nil:
			text = m.Venue.Title
		case m.Contact != nil:
			text = contactName(m.Contact)
		case m.Dice != nil:
			text = m.Dice.Emoji + " " + strconv.Itoa(m.Dice.Value)
		case m.Poll != nil:
			text = m.Poll.Question
		}
	}

	return Message{
		ID:            m.ID,
		ChatID:        m.Chat.ID,
		Text:          text,
		From:          author(m),
		FromBot:       m.From != nil && m.From.ID == k.botUser().ID,
		ForwardedFrom: forwardedFrom(m.ForwardOrigin),
		Media:         media,
		FileID:        fileIn(&m),
		Album:         m.MediaGroupID,
		Entities:      entitiesOf(text, entities),
		rich:          markdownOf(text, entities),
		carries:       k.carried(&m),
		Options:       pollOptions(m.Poll),
		Reactions:     k.reactions.on(m.Chat.ID, m.ID),
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
