package kitchen

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestMessageRendersTextAndKeyboard(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7, WithFullName("Ada", "Lovelace"))
	user.Send("hi")

	if got := user.Screen().String(); got != "menu\n[English] [فارسی]" {
		t.Errorf("screen =\n%s\nwant the text above its keyboard", got)
	}
}

func TestMessageRendersWhatItCarries(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	user := k.User(7)
	user.SendPhoto("cat.jpg", []byte("bytes"), "look")
	if got := user.Screen().String(); got != "(photo) look" {
		t.Errorf("screen = %q, want the caption marked as a photo", got)
	}

	user.ShareLocation(35.7, 51.4)
	if got := user.Screen().String(); got != "(location)" {
		t.Errorf("screen = %q, want the attachment named", got)
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
		"**Concierge:** menu\n[English] [فارسی]",
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
