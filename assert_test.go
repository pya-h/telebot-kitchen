package kitchen

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// twiceBot answers every message twice, the shape of a double-send bug.
func twiceBot(t *testing.T, k *Kitchen) *bot.Bot {
	t.Helper()
	return syncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		for _, text := range []string{"first", "second"} {
			b.SendMessage(ctx, &bot.SendMessageParams{ChatID: u.Message.Chat.ID, Text: text})
		}
	})
}

func TestExpectFindsTheCall(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")

	call := k.Expect(Method("sendMessage"), ToUser(user), HasButton("English"))
	if call.Text() != "menu" {
		t.Errorf("call = %+v, want the menu the bot sent", call)
	}
	k.ExpectNo(Method("deleteMessage"))
	k.ExpectCount(1, Method("sendMessage"))
}

func TestExpectReportsWhatTheBotSentInstead(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)
	k.User(7).Send("hi")

	k.Expect(Method("sendMessage"), TextIs("welcome"))

	errs := tb.errors()
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", errs)
	}
	if !strings.Contains(errs[0], `method sendMessage and text "welcome"`) {
		t.Errorf("error = %q, want it to name what was expected", errs[0])
	}
	if !strings.Contains(errs[0], `sendMessage to chat 7 "menu"`) {
		t.Errorf("error = %q, want the record of what the bot did instead", errs[0])
	}
}

func TestUserExpectReadsTheReply(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Expect(TextIs("menu"), HasButton("lang:fa"))

	user.Tap("English")
	user.Expect(TextContains("lang:en"))
}

func TestUserExpectReportsTheMismatch(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Expect(HasButton("Deutsch"))

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], `"English", "فارسی"`) {
		t.Errorf("errors = %v, want the buttons the user actually had", errs)
	}
}

func TestExpectScreenReadsTheCurrentView(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	screen := user.ExpectScreen(TextIs("menu"), HasButton("English"))
	if len(screen.Buttons()) != 2 {
		t.Errorf("screen = %+v, want both language buttons", screen)
	}
}

func TestExpectNothingMoreCatchesADoubleSend(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(twiceBot(t, k).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Expect(TextIs("first"))
	user.ExpectNothingMore()

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], `"second"`) {
		t.Errorf("errors = %v, want the unexpected second message named", errs)
	}
}

func TestExpectNothingMorePassesOnceEveryReplyIsRead(t *testing.T) {
	k := New(t)
	k.DeliverTo(twiceBot(t, k).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Expect(TextIs("first"))
	user.Expect(TextIs("second"))
	user.ExpectNothingMore()
}

func TestScenarioRunsStepsInOrder(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)
	user := k.User(7)

	var ran []string
	k.Scenario(
		Step{Name: "open the menu", Do: func() {
			ran = append(ran, "open")
			user.Send("hi")
			user.Expect(HasButton("English"))
		}},
		Step{Name: "pick a language", Do: func() {
			ran = append(ran, "pick")
			user.Tap("English")
			user.Expect(TextContains("lang:en"))
		}},
	)

	if strings.Join(ran, ",") != "open,pick" {
		t.Errorf("ran = %v, want both steps in order", ran)
	}
}

func TestScenarioSkipsTheStepsAfterAFailure(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	var ran []string
	k.Scenario(
		Step{Name: "first", Do: func() { ran = append(ran, "first") }},
		Step{Name: "second", Do: func() {
			ran = append(ran, "second")
			tb.Errorf("boom")
		}},
		Step{Name: "third", Do: func() { ran = append(ran, "third") }},
		Step{Name: "fourth", Do: func() { ran = append(ran, "fourth") }},
	)

	if strings.Join(ran, ",") != "first,second" {
		t.Errorf("ran = %v, want nothing after the step that failed", ran)
	}
	errs := tb.errors()
	if len(errs) != 2 || !strings.Contains(errs[1], `"third", "fourth"`) {
		t.Errorf("errors = %v, want the skipped steps named", errs)
	}
}
