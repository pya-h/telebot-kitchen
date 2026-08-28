package kitchen

import (
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

func TestMessageIDsRunPerChat(t *testing.T) {
	k := New(t)

	first := k.world.add(101, models.Message{Text: "one"})
	second := k.world.add(101, models.Message{Text: "two"})
	other := k.world.add(202, models.Message{Text: "elsewhere"})

	if first.ID != 1 || second.ID != 2 {
		t.Errorf("message ids = %d, %d; want 1, 2", first.ID, second.ID)
	}
	if other.ID != 1 {
		t.Errorf("second chat started at %d, want its own sequence from 1", other.ID)
	}
	if other.Chat.ID != 202 || other.Chat.Type != models.ChatTypePrivate {
		t.Errorf("chat = %+v, want the private chat it was added to", other.Chat)
	}
}

func TestHistoryKeepsOrder(t *testing.T) {
	k := New(t)
	k.world.add(101, models.Message{Text: "one"})
	k.world.add(101, models.Message{Text: "two"})

	log := k.world.history(101)
	if len(log) != 2 || log[0].Text != "one" || log[1].Text != "two" {
		t.Fatalf("history = %+v, want both messages in order", log)
	}

	latest, ok := k.world.latest(101)
	if !ok || latest.Text != "two" {
		t.Errorf("latest = %+v, %v; want the last message", latest, ok)
	}
	if _, ok := k.world.latest(999); ok {
		t.Error("latest reported a message for an untouched chat")
	}
}

func TestHistoryIsACopy(t *testing.T) {
	k := New(t)
	k.world.add(101, models.Message{Text: "one"})

	k.world.history(101)[0].Text = "tampered"
	if latest, _ := k.world.latest(101); latest.Text != "one" {
		t.Errorf("stored text = %q, want the log untouched by the caller", latest.Text)
	}
}

func TestMessagesCarryKitchenTime(t *testing.T) {
	start := time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC)
	k := New(t, WithStartTime(start))

	if m := k.world.add(101, models.Message{}); m.Date != int(start.Unix()) {
		t.Errorf("date = %d, want the kitchen's start time %d", m.Date, start.Unix())
	}

	k.Clock().Advance(90 * time.Second)
	if m := k.world.add(101, models.Message{}); m.Date != int(start.Add(90*time.Second).Unix()) {
		t.Errorf("date = %d, want the advanced time", m.Date)
	}
}

func TestUpdateIDsAreMonotonic(t *testing.T) {
	k := New(t)
	if first, second := k.world.nextUpdate(), k.world.nextUpdate(); first != 1 || second != 2 {
		t.Errorf("update ids = %d, %d; want 1, 2", first, second)
	}
}
