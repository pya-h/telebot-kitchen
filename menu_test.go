package kitchen

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var mainMenu = &models.ReplyKeyboardMarkup{Keyboard: [][]models.KeyboardButton{
	{{Text: "Search"}, {Text: "Profile"}},
}}

func raise(t *testing.T, b *bot.Bot, chatID int64, text string, markup any) {
	t.Helper()
	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: chatID, Text: text, ReplyMarkup: markup,
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
}

// The point of a hard keyboard: it outlives the message that raised it.
func TestAMenuOutlivesTheMessageThatRaisedIt(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7, Started())

	raise(t, b, ada.ChatID(), "menu", mainMenu)
	raise(t, b, ada.ChatID(), "searching…", nil)

	want := [][]string{{"Search", "Profile"}}
	if got := ada.Menu(); !reflect.DeepEqual(got, want) {
		t.Errorf("menu = %v, want %v still up", got, want)
	}
}

func TestAMenuIsReplacedByTheNextOne(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7, Started())

	raise(t, b, ada.ChatID(), "menu", mainMenu)
	raise(t, b, ada.ChatID(), "in chat", &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{{{Text: "Next"}, {Text: "Close"}}},
	})

	if ada.HasKey("Search") || !ada.HasKey("Close") {
		t.Errorf("menu = %v, want only the newer keys", ada.Menu())
	}
}

func TestRemovingTheMenuTakesEveryKey(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7, Started())

	raise(t, b, ada.ChatID(), "menu", mainMenu)
	raise(t, b, ada.ChatID(), "gone", &models.ReplyKeyboardRemove{RemoveKeyboard: true})

	if got := ada.Menu(); len(got) != 0 {
		t.Errorf("menu = %v, want nothing under the compose box", got)
	}
}

// The two kinds share reply_markup, so neither may be read as the other.
func TestAMenuIsNotAnInlineKeyboard(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7, Started())

	raise(t, b, ada.ChatID(), "menu", mainMenu)
	if buttons := ada.Screen().Buttons(); len(buttons) != 0 {
		t.Errorf("buttons = %v, want a hard key to stay off the message", buttons)
	}

	raise(t, b, ada.ChatID(), "pick", testKeyboard)
	if got := ada.Menu(); !reflect.DeepEqual(got, [][]string{{"Search", "Profile"}}) {
		t.Errorf("menu = %v, want an inline keyboard to leave it alone", got)
	}
}

func TestPressingAKeySendsItsLabel(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var got *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = u.Message })
	ada := k.User(7, Started())

	raise(t, b, ada.ChatID(), "menu", mainMenu)
	ada.Press("Search")

	if got == nil || got.Text != "Search" {
		t.Errorf("update = %+v, want the label typed out", got)
	}
}

func TestPressingAKeyThatIsNotThereFails(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7, Started())

	raise(t, b, ada.ChatID(), "menu", mainMenu)
	ada.Press("Settings")

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], `"Search", "Profile"`) {
		t.Errorf("errors = %v, want one naming the keys that are there", errs)
	}
}

func TestASendTheChatRefusesLeavesTheMenuAlone(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7).In(k.Group(-100, "Standup"))

	raise(t, b, ada.ChatID(), "menu", mainMenu)
	ada.RemoveBot()
	b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID:      ada.ChatID(),
		Text:        "gone",
		ReplyMarkup: &models.ReplyKeyboardRemove{RemoveKeyboard: true},
	})

	if !ada.HasKey("Search") {
		t.Errorf("menu = %v, want the keys a refused send never took away", ada.Menu())
	}
}

func TestAMigratedGroupKeepsItsMenu(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-100, "Standup")

	raise(t, b, team.ID(), "menu", mainMenu)
	moved := team.MigrateToSupergroup(-1001)

	if got := moved.Menu(); !reflect.DeepEqual(got, [][]string{{"Search", "Profile"}}) {
		t.Errorf("menu = %v, want the one the group had", got)
	}
}
