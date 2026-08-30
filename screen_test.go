package kitchen

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestScreenShowsTheNewestMessage(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")

	screen := user.Screen()
	if screen.Text != "menu" || !screen.FromBot {
		t.Errorf("screen = %+v, want the bot's menu", screen)
	}
	if !screen.HasButton("English") || !screen.HasButton("lang:fa") {
		t.Errorf("buttons = %+v, want both by label and by data", screen.Buttons())
	}
	if button, ok := screen.Button("فارسی"); !ok || button.Data != "lang:fa" {
		t.Errorf("button = %+v, %v; want the data behind the label", button, ok)
	}
	if screen.Sent != k.Clock().Now() {
		t.Errorf("sent = %v, want the kitchen clock %v", screen.Sent, k.Clock().Now())
	}
}

func TestScreenKeepsTheKeyboardGrid(t *testing.T) {
	k := New(t)
	rows := [][]models.InlineKeyboardButton{
		{{Text: "Yes", CallbackData: "y"}, {Text: "No", CallbackData: "n"}},
		{{Text: "Docs", URL: "https://example.test"}},
	}
	k.DeliverTo(menuBot(t, k, rows...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	screen := user.Screen()

	if len(screen.Keyboard) != 2 || len(screen.Keyboard[0]) != 2 || len(screen.Keyboard[1]) != 1 {
		t.Fatalf("keyboard = %+v, want the rows as laid out", screen.Keyboard)
	}
	if len(screen.Buttons()) != 3 {
		t.Errorf("flattened = %+v, want every button in reading order", screen.Buttons())
	}
	if button, _ := screen.Button("Docs"); button.URL != "https://example.test" {
		t.Errorf("button = %+v, want the link it opens", button)
	}
}

// A caption is the only text a photo message has, so a screen reads it as one.
func TestScreenReadsACaptionAsText(t *testing.T) {
	k := New(t)
	user := k.User(7)
	k.world.add(user.ChatID(), models.Message{Caption: "look at this"})

	if got := user.Screen().Text; got != "look at this" {
		t.Errorf("text = %q, want the caption", got)
	}
}

func TestScreenOfAnUntouchedChatIsEmpty(t *testing.T) {
	k := New(t)
	if screen := k.User(7).Screen(); screen.Text != "" || screen.HasButton("English") {
		t.Errorf("screen = %+v, want nothing on it", screen)
	}
}

func TestHistoryReadsBothSides(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Tap("English")

	// A tap is a callback query, not a message, so it adds nothing of its own.
	log := user.History()
	if len(log) != 3 {
		t.Fatalf("history = %+v, want the greeting, the menu, and the tap's echo", log)
	}
	if log[0].Text != "hi" || log[0].FromBot {
		t.Errorf("first = %+v, want the user's own message", log[0])
	}
	if log[1].Text != "menu" || !log[1].FromBot || !log[1].HasButton("English") {
		t.Errorf("second = %+v, want the bot's menu", log[1])
	}
	if log[2].Text != "tapped: lang:en" {
		t.Errorf("last = %+v, want the reply to the tap", log[2])
	}
	if same := k.History(user.ChatID()); len(same) != len(log) {
		t.Errorf("chat history = %+v, want the user's own", same)
	}
}
