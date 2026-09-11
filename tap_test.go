package kitchen

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// menuBot answers text with a keyboard and echoes back whatever button is tapped.
func tapping(data string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Go", CallbackData: data}}}}
}

func TestCallbackDataIsMeasuredInBytes(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ctx := context.Background()
	ada := k.User(7, Started())

	for _, c := range []struct {
		name, data string
		fits       bool
	}{
		{"at the limit", strings.Repeat("a", mostCallbackData), true},
		{"one over", strings.Repeat("a", mostCallbackData+1), false},
		{"32 Persian letters", strings.Repeat("س", mostCallbackData/2), true},
		{"33 Persian letters", strings.Repeat("س", mostCallbackData/2+1), false},
	} {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: ada.ID(), Text: c.name, ReplyMarkup: tapping(c.data)})
		if fits := err == nil; fits != c.fits {
			t.Errorf("%s: err = %v, want it to fit: %v", c.name, err, c.fits)
		}
		if err != nil && !strings.Contains(err.Error(), "BUTTON_DATA_INVALID") {
			t.Errorf("%s: err = %v, want Telegram's refusal", c.name, err)
		}
	}

	// The library leaves empty data off, so only a raw call can send it.
	reply := callJSON(t, k, "sendMessage", fmt.Sprintf(
		`{"chat_id":%d,"text":"x","reply_markup":{"inline_keyboard":[[{"text":"Go","callback_data":""}]]}}`, ada.ID()))
	if reply.OK || !strings.Contains(reply.Description, "BUTTON_DATA_INVALID") {
		t.Errorf("empty data = %+v, want it refused", reply)
	}

	_, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID: ada.ID(), MessageID: ada.Screen().ID, ReplyMarkup: tapping(strings.Repeat("a", mostCallbackData+1)),
	})
	if err == nil || !strings.Contains(err.Error(), "BUTTON_DATA_INVALID") {
		t.Errorf("edit: err = %v, want data over the limit refused", err)
	}
}

func TestAResultsButtonsAreHeldToTheSameBytes(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var err error
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		_, err = b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
			InlineQueryID: u.InlineQuery.ID,
			Results: []models.InlineQueryResult{&models.InlineQueryResultArticle{
				ID: "1", Title: "Go", InputMessageContent: &models.InputTextMessageContent{MessageText: "go"},
				ReplyMarkup: tapping(strings.Repeat("a", mostCallbackData+1)),
			}},
		})
	})

	k.User(7).Search("go")
	if err == nil || !strings.Contains(err.Error(), "BUTTON_DATA_INVALID") {
		t.Errorf("err = %v, want a result carrying data over the limit refused", err)
	}
}

func TestAnAnswerToATapHoldsTwoHundredCharacters(t *testing.T) {
	b := newClient(t, New(t))
	ctx := context.Background()

	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: "1", Text: strings.Repeat("س", mostAnswer),
	}); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	_, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: "2", Text: strings.Repeat("a", mostAnswer+1), ShowAlert: true,
	})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("one over: err = %v, want it refused", err)
	}
}

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
