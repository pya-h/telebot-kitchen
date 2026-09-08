package kitchen

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// polling starts a real bot fetching its own updates, and stops it with the test.
func polling(t *testing.T, k *Kitchen, handle bot.HandlerFunc) {
	t.Helper()
	k.DeliverByPolling()

	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()), bot.WithDefaultHandler(handle))
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Start(ctx)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
}

func TestABotFetchesItsOwnUpdates(t *testing.T) {
	k := New(t, WithWaitTimeout(500*time.Millisecond))
	polling(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		if u.Message == nil {
			return
		}
		b.SendMessage(ctx, &bot.SendMessageParams{ChatID: u.Message.Chat.ID, Text: "echo: " + u.Message.Text})
	})
	ada := k.User(7)

	ada.Send("hi")
	if reply := ada.ExpectReply(); reply.Text != "echo: hi" {
		t.Errorf("reply = %s, want the echo", reply)
	}

	ada.Send("again")
	if reply := ada.ExpectReply(); reply.Text != "echo: again" {
		t.Errorf("reply = %s, want the second echo", reply)
	}
}

func TestAPollHandsOutWhatIsWaitingAndKeepsItUntilConfirmed(t *testing.T) {
	k := New(t, WithWaitTimeout(150*time.Millisecond))
	k.DeliverByPolling()
	ada := k.User(7)
	ada.Send("one")
	ada.Send("two")

	first := poll(t, k, map[string]string{})
	if len(first) != 2 {
		t.Fatalf("updates = %d, want both waiting ones", len(first))
	}
	if first[0].Message.Text != "one" || first[1].Message.Text != "two" {
		t.Errorf("updates = %+v, want them in the order they were said", first)
	}

	// Nothing was confirmed, so the same two come back.
	again := poll(t, k, map[string]string{})
	if len(again) != 2 {
		t.Errorf("updates = %d, want the unconfirmed ones handed out again", len(again))
	}

	// Confirming the first leaves the second.
	left := poll(t, k, map[string]string{"offset": itoa(first[1].ID)})
	if len(left) != 1 || left[0].Message.Text != "two" {
		t.Errorf("updates = %+v, want only what was not confirmed", left)
	}
	if none := poll(t, k, map[string]string{"offset": itoa(first[1].ID + 1)}); len(none) != 0 {
		t.Errorf("updates = %+v, want nothing left once both are confirmed", none)
	}
	// Confirmed means gone, not merely skipped: a long conversation would grow
	// without bound if the queue only filtered on the way out.
	if held := k.updates.held(); held != 0 {
		t.Errorf("queue holds %d updates, want the confirmed ones dropped", held)
	}
}

func TestAPollTakesAtMostWhatItAsksFor(t *testing.T) {
	k := New(t)
	k.DeliverByPolling()
	ada := k.User(7)
	ada.Send("one")
	ada.Send("two")
	ada.Send("three")

	if got := poll(t, k, map[string]string{"limit": "2"}); len(got) != 2 {
		t.Errorf("updates = %d, want the two it asked for", len(got))
	}
}

func TestAPollWithNothingComingAnswersEmpty(t *testing.T) {
	k := New(t, WithWaitTimeout(150*time.Millisecond))
	k.DeliverByPolling()

	started := time.Now()
	got := poll(t, k, map[string]string{"timeout": "30"})
	waited := time.Since(started)

	if len(got) != 0 {
		t.Errorf("updates = %+v, want none", got)
	}
	if waited < 100*time.Millisecond {
		t.Errorf("the poll answered after %s, want it to wait for something first", waited)
	}
	if waited > time.Second {
		t.Errorf("the poll waited %s, want the kitchen's bound rather than the thirty seconds asked for", waited)
	}
}

// A poll is the bot asking whether anything happened. If it counted as
// something happening, a bot polling in a tight loop would keep the
// conversation from ever going quiet.
func TestAPollDoesNotWakeWaiters(t *testing.T) {
	k := New(t, WithWaitTimeout(100*time.Millisecond))
	k.DeliverByPolling()

	wake := k.activity.watch()
	poll(t, k, map[string]string{})

	select {
	case <-wake:
		t.Error("a poll woke the waiters, want it to count as nothing happening")
	default:
	}
}

func TestAPollHonoursAShorterTimeoutThanTheBound(t *testing.T) {
	k := New(t, WithWaitTimeout(4*time.Second))
	k.DeliverByPolling()

	started := time.Now()
	poll(t, k, map[string]string{"timeout": "1"})
	if waited := time.Since(started); waited > 2*time.Second {
		t.Errorf("the poll waited %s, want about the one second it asked for", waited)
	}
}

func TestAPollWakesWhenSomethingArrives(t *testing.T) {
	k := New(t)
	k.DeliverByPolling()
	ada := k.User(7)

	go func() {
		time.Sleep(30 * time.Millisecond)
		ada.Send("late")
	}()

	got := poll(t, k, map[string]string{"timeout": "30"})
	if len(got) != 1 || got[0].Message.Text != "late" {
		t.Errorf("updates = %+v, want the one that arrived while the poll waited", got)
	}
}

func TestASecondPollerIsToldItIsTheSecond(t *testing.T) {
	k := New(t, WithWaitTimeout(300*time.Millisecond))
	k.DeliverByPolling()

	inFlight := make(chan struct{})
	go func() {
		defer close(inFlight)
		poll(t, k, map[string]string{"timeout": "30"})
	}()

	// A poll notes no activity, so waiting on one means watching for it rather
	// than being woken by it.
	if !within(time.Second, k.updates.busy) {
		t.Fatal("the first poll never started")
	}
	reply := callForm(t, k, "getUpdates", map[string]string{})
	if reply.OK || reply.ErrorCode != 409 {
		t.Errorf("reply = %+v, want a conflict", reply)
	}
	if !strings.Contains(reply.Description, "other getUpdates") {
		t.Errorf("description = %q, want it to say another poll holds the queue", reply.Description)
	}
	<-inFlight
}

func TestPollingAndAWebhookAreNotBothTheWayIn(t *testing.T) {
	k := New(t, WithWaitTimeout(150*time.Millisecond))
	k.DeliverByPolling()

	if reply := callForm(t, k, "setWebhook", map[string]string{"url": "https://example.test/hook"}); !reply.OK {
		t.Fatalf("setWebhook = %+v", reply)
	}
	reply := callForm(t, k, "getUpdates", map[string]string{})
	if reply.OK || reply.ErrorCode != 409 {
		t.Errorf("reply = %+v, want a conflict while the webhook is up", reply)
	}

	callForm(t, k, "deleteWebhook", map[string]string{})
	if reply := callForm(t, k, "getUpdates", map[string]string{}); !reply.OK {
		t.Errorf("reply = %+v, want polling to work once the webhook is gone", reply)
	}
}

func TestPollingAKitchenThatPushesIsReportedOnce(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb, WithWaitTimeout(100*time.Millisecond))
	k.DeliverTo(func(context.Context, *models.Update) {})

	callForm(t, k, "getUpdates", map[string]string{})
	callForm(t, k, "getUpdates", map[string]string{})

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "DeliverByPolling") {
		t.Errorf("errors = %v, want one telling the test which mode it forgot", errs)
	}
}

func TestAPollIsNotSomethingHappening(t *testing.T) {
	k := New(t, WithWaitTimeout(100*time.Millisecond))
	k.DeliverByPolling()
	ada := k.User(7)
	ada.Send("hi")

	poll(t, k, map[string]string{})
	// Settling would never end if every poll counted as the conversation moving.
	k.Settle()

	if calls := k.Calls(); len(calls) != 0 {
		t.Errorf("calls = %s, want a poll to stay out of the log", calls)
	}
}

// within spins until cond holds, for the one thing the kitchen deliberately
// does not announce.
func within(limit time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func poll(t *testing.T, k *Kitchen, form map[string]string) []models.Update {
	t.Helper()
	var got []models.Update
	reply := callForm(t, k, "getUpdates", form)
	if !reply.OK {
		t.Fatalf("getUpdates = %+v", reply)
	}
	reply.decode(t, &got)
	return got
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
