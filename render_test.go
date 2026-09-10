package kitchen

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestMessageRendersTextAndKeyboard(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7, WithFullName("Ada", "Lovelace"))
	user.Send("hi")

	// The Persian label is fenced off, or it would take the brackets with it.
	if got := user.Screen().String(); got != "menu\n[English] [⁨فارسی⁩]" {
		t.Errorf("screen =\n%s\nwant the text above its keyboard", got)
	}
}

func TestMessageRendersWhatItCarries(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	user := k.User(7)
	user.SendPhoto("cat.jpg", []byte("bytes"), "look")
	if got := user.Screen().String(); got != "(photo cat.jpg) look" {
		t.Errorf("screen = %q, want the caption marked as the photo it names", got)
	}

	user.ShareLocation(35.7, 51.4)
	if got := user.Screen().String(); got != "(location 35.7000, 51.4000)" {
		t.Errorf("screen = %q, want the placeholder to say where", got)
	}

	if got := (Message{}).String(); got != "(nothing)" {
		t.Errorf("empty message = %q, want it to say so", got)
	}
}

func TestTranscriptReadsAsAConversation(t *testing.T) {
	k := New(t, WithBotName("Concierge"))
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7, WithFullName("Ada", "Lovelace"))
	user.Send("hi")
	user.Tap("English")

	want := strings.Join([]string{
		"**Ada Lovelace:** hi",
		"**Concierge:** menu\n[English] [⁨فارسی⁩]",
		"**Concierge:** tapped: lang:en",
	}, "\n\n") + "\n"

	if got := user.Transcript(); got != want {
		t.Errorf("transcript =\n%s\nwant\n%s", got, want)
	}
}

// A channel has no sender of its own kind, so a client signs its posts with the
// channel, and a service message nobody wrote carries no name at all.
func TestATranscriptNamesWhoeverWrote(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	news := k.Channel(-1002, "Releases")
	news.Post("v1 is out")

	if got, want := news.Transcript(), "**Releases:** v1 is out\n"; got != want {
		t.Errorf("transcript = %q, want %q", got, want)
	}

	team := k.Group(-42, "Standup")
	team.MigrateToSupergroup(-1042)
	if got, want := team.Transcript(), "(moved)\n"; got != want {
		t.Errorf("transcript = %q, want %q", got, want)
	}
}

// Two photos in a row read as one line twice unless the placeholder says which.
func TestAPlaceholderSaysWhatItCarries(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)

	ada.SendPhoto("lunch.jpg", []byte("bytes"), "")
	ada.SendPhoto("dinner.jpg", []byte("bytes"), "")
	ada.SendDocument("terms.pdf", []byte("bytes"), "sign here")
	ada.SendVideoNote("wave.mp4", []byte("bytes"))
	ada.ShareVenue(35.7, 51.4, "Rossi", "12 Main St")

	var shown []string
	for _, m := range ada.History() {
		shown = append(shown, m.String())
	}
	want := []string{
		"(photo lunch.jpg)",
		"(photo dinner.jpg)",
		"(document terms.pdf) sign here",
		"(video note wave.mp4)",
		"(venue) Rossi",
	}
	if !slices.Equal(shown, want) {
		t.Errorf("transcript lines =\n%q\nwant\n%q", shown, want)
	}
}

// A reply that runs the other way would otherwise take the frame around it with
// it, and the buttons would read back to front.
func TestATranscriptKeepsItsShapeAroundRightToLeftText(t *testing.T) {
	k := New(t, WithBotName("پذیرش"))
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7, WithFullName("آدا", "لاولیس"))

	ada.Send("سلام")
	got := ada.Transcript()

	want := "**⁨آدا لاولیس⁩:** ⁨سلام⁩\n"
	if got != want {
		t.Errorf("transcript = %q, want the name and the reply each fenced off", got)
	}

	// Nothing is fenced off that does not need it, so a plain transcript keeps
	// reading as plain bytes and an existing golden file still matches.
	bob := k.User(8)
	bob.Send("hello")
	if plain := bob.Transcript(); strings.ContainsAny(plain, "⁨⁩") {
		t.Errorf("transcript = %q, want no fences around text that needs none", plain)
	}
}
