package example

import (
	"path/filepath"
	"testing"

	kitchen "github.com/pya-h/telebot-kitchen"
)

// The bot handles updates on its own goroutine, as the library does by default,
// so every assertion below waits for the bot rather than assuming it is done.
func start(t *testing.T) (*kitchen.Kitchen, *kitchen.User) {
	t.Helper()

	k := kitchen.New(t, kitchen.WithBotName("Settings Bot"))
	b, err := New(k.APIURL(), k.Token())
	if err != nil {
		t.Fatalf("build the bot: %v", err)
	}
	k.DeliverTo(b.ProcessUpdate)

	return k, k.User(101, kitchen.WithFullName("Ada", "Lovelace"))
}

func TestUnknownTextIsEchoed(t *testing.T) {
	k, ada := start(t)

	ada.Send("hello")
	ada.Expect(kitchen.TextIs("echo: hello"))
	ada.ExpectNothingMore()

	k.ExpectNo(kitchen.Method("editMessageText"))
}

func TestSettingsFlow(t *testing.T) {
	k, ada := start(t)

	k.Scenario(
		kitchen.Step{Name: "open the menu", Do: func() {
			ada.SendCommand("start")
			ada.Expect(
				kitchen.TextContains("Pick a language"),
				kitchen.HasButton("English"),
				kitchen.HasButton("lang:fa"),
			)
		}},
		kitchen.Step{Name: "pick a language", Do: func() {
			ada.Tap("English")
			ada.ExpectScreen(
				kitchen.TextContains("English"),
				kitchen.HasButton("Turn notifications on"),
			)
		}},
		kitchen.Step{Name: "turn notifications on", Do: func() {
			ada.Tap("Turn notifications on")
			ada.ExpectScreen(
				kitchen.TextContains("notifications on"),
				kitchen.HasButton("Turn notifications off"),
			)
		}},
		kitchen.Step{Name: "finish", Do: func() {
			ada.Tap("Done")
			ada.ExpectScreen(kitchen.TextIs("All set: English, notifications on"))
		}},
	)

	// The whole flow lives in one message, and every tap was acknowledged.
	k.ExpectCount(1, kitchen.Method("sendMessage"))
	k.ExpectCount(3, kitchen.Method("answerCallbackQuery"))
}

func TestSettingsAreKeptPerUser(t *testing.T) {
	k, ada := start(t)
	grace := k.User(102, kitchen.WithFullName("Grace", "Hopper"))

	for _, user := range []*kitchen.User{ada, grace} {
		user.SendCommand("start")
		user.Expect(kitchen.HasButton("English"))
	}

	ada.Tap("فارسی")
	ada.ExpectScreen(kitchen.TextContains("فارسی"))

	grace.Tap("English")
	grace.ExpectScreen(kitchen.TextContains("English"))

	// Ada acts last, so a setting leaking between chats would surface as Grace's
	// language on Ada's screen.
	ada.Tap("Turn notifications on")
	ada.ExpectScreen(kitchen.TextIs("Settings: فارسی, notifications on"))

	k.ExpectCount(1, kitchen.Method("editMessageText"), kitchen.ToUser(grace))
}

func TestTheMenuIsEditedRatherThanResent(t *testing.T) {
	_, ada := start(t)

	ada.SendCommand("start")
	menu := ada.Expect(kitchen.HasButton("English"))

	ada.Tap("English")
	ada.ExpectScreen(kitchen.HasButton("Done"))

	if screen := ada.Screen(); screen.ID != menu.ID {
		t.Errorf("screen id = %d, want the menu %d edited in place", screen.ID, menu.ID)
	}
	if log := ada.History(); len(log) != 2 {
		t.Errorf("history = %d entries, want the command and the one menu", len(log))
	}
}

// The chat as a reader would see it at the end. Because the menu is edited in
// place, the transcript shows where it landed, not every version it passed
// through. Rerun with -kitchen.update after changing any wording.
func TestTheWholeConversationReads(t *testing.T) {
	_, ada := start(t)

	ada.SendCommand("start")
	ada.Expect(kitchen.HasButton("English"))

	ada.Tap("English")
	ada.ExpectScreen(kitchen.HasButton("Done"))

	ada.Tap("Turn notifications on")
	ada.ExpectScreen(kitchen.TextContains("notifications on"))

	ada.Tap("Done")
	ada.ExpectScreen(kitchen.TextContains("All set"))

	ada.ExpectTranscript(filepath.Join("testdata", "settings.md"))
}
