package kitchen

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// menuBot answers text with a keyboard and echoes back whatever button is tapped.
func menuBot(t *testing.T, k *Kitchen, rows ...[]models.InlineKeyboardButton) *bot.Bot {
	t.Helper()
	return syncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		if u.CallbackQuery != nil {
			b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: u.CallbackQuery.Message.Message.Chat.ID,
				Text:   "tapped: " + u.CallbackQuery.Data,
			})
			return
		}
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      u.Message.Chat.ID,
			Text:        "menu",
			ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: rows},
		})
	})
}

var languageMenu = [][]models.InlineKeyboardButton{{
	{Text: "English", CallbackData: "lang:en"},
	{Text: "فارسی", CallbackData: "lang:fa"},
}}

func TestTapByLabel(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Tap("فارسی")

	if reply, ok := k.world.latest(user.ChatID()); !ok || reply.Text != "tapped: lang:fa" {
		t.Errorf("reply = %+v, want the callback data behind the label", reply)
	}
}

// Callback data survives a change of wording, so a test may target it directly.
func TestTapByCallbackData(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Tap("lang:en")

	if reply, ok := k.world.latest(user.ChatID()); !ok || reply.Text != "tapped: lang:en" {
		t.Errorf("reply = %+v, want the tapped button", reply)
	}
}

func TestTapAnswersTheQueryItIssued(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Tap("English")

	answers := k.CallbackAnswers()
	if len(answers) != 1 {
		t.Fatalf("answers = %+v, want the one the bot sent", answers)
	}
	if _, ok := k.CallbackAnswer(answers[0].QueryID); !ok {
		t.Errorf("answer %q is not addressable by its query id", answers[0].QueryID)
	}
}

func TestTapReachesOnlyTheNewestKeyboard(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	defer tb.close()
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Tap("English") // draws a second menu, and the callback echo after it

	k.world.add(user.ChatID(), models.Message{
		Text:        "newer",
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Back", CallbackData: "back"}}}},
	})
	user.Tap("English")

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], `no button "English"`) || !strings.Contains(errs[0], `"Back"`) {
		t.Errorf("reported = %v, want one naming the missing button and what is on screen", errs)
	}
}

func TestScrollbackReachesOlderKeyboards(t *testing.T) {
	k := New(t, WithScrollback())
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	k.world.add(user.ChatID(), models.Message{
		Text:        "newer",
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Back", CallbackData: "back"}}}},
	})
	user.Tap("English")

	if reply, ok := k.world.latest(user.ChatID()); !ok || reply.Text != "tapped: lang:en" {
		t.Errorf("reply = %+v, want the older button to still answer", reply)
	}
}

func TestTapOnAURLButtonReports(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	defer tb.close()
	k.DeliverTo(menuBot(t, k, []models.InlineKeyboardButton{{Text: "Docs", URL: "https://example.test"}}).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Tap("Docs")

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "no callback data") {
		t.Errorf("reported = %v, want one explaining a URL button never reaches the bot", errs)
	}
}

func TestTapWithoutAnyKeyboardReports(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	defer tb.close()
	k.DeliverTo(func(context.Context, *models.Update) {})

	k.User(7).Tap("English")

	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "no buttons on screen") {
		t.Errorf("reported = %v, want one naming the empty screen", errs)
	}
}
