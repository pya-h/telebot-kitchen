package kitchen

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func article(id, title, text string) models.InlineQueryResult {
	return &models.InlineQueryResultArticle{
		ID: id, Title: title,
		InputMessageContent: &models.InputTextMessageContent{MessageText: text},
	}
}

// answering is a bot that answers every search with the results it is given.
func offering(t *testing.T, k *Kitchen, b *bot.Bot, results ...models.InlineQueryResult) {
	t.Helper()
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery == nil {
			return
		}
		if _, err := b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
			InlineQueryID: u.InlineQuery.ID, Results: results,
		}); err != nil {
			t.Errorf("answer: %v", err)
		}
	})
}

func TestASearchReachesTheBotAndIsNotAMessage(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var asked []*models.InlineQuery
	messages := 0
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery != nil {
			asked = append(asked, u.InlineQuery)
			b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
				InlineQueryID: u.InlineQuery.ID, Results: []models.InlineQueryResult{article("1", "Pizza", "a pizza")},
			})
		}
		if u.Message != nil {
			messages++
		}
	})
	team := k.Group(-42, "Standup")
	ada := k.User(7).In(team)
	ada.Join()
	k.Settle()
	messages = 0

	ada.Search("piz")
	k.Settle()

	if len(asked) != 1 || asked[0].Query != "piz" || asked[0].From.ID != 7 {
		t.Fatalf("queries = %+v, want ada's search", asked)
	}
	if asked[0].ChatType != string(models.ChatTypeGroup) {
		t.Errorf("query = %+v, want it to say where she was typing", asked[0])
	}
	if messages != 0 {
		t.Errorf("messages = %d, want a search to reach the bot as a query and nothing else", messages)
	}
	if log := team.History(); len(log) != 1 {
		t.Errorf("history = %v, want only the join, since a search puts nothing in the chat", log)
	}
}

func TestPickingLandsTheMessageTheResultBecomes(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var chosen []*models.ChosenInlineResult
	offering(t, k, b, article("pizza", "Pizza", "a pizza, then"), article("pasta", "Pasta", "a pasta, then"))
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery != nil {
			b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
				InlineQueryID: u.InlineQuery.ID,
				Results: []models.InlineQueryResult{
					article("pizza", "Pizza", "a pizza, then"),
					article("pasta", "Pasta", "a pasta, then"),
				},
			})
		}
		if u.ChosenInlineResult != nil {
			chosen = append(chosen, u.ChosenInlineResult)
		}
	})
	ada := k.User(7)

	ada.Search("p")
	k.Settle()
	ada.Pick("Pasta")
	k.Settle()

	if len(chosen) != 1 || chosen[0].ResultID != "pasta" || chosen[0].Query != "p" {
		t.Fatalf("chosen = %+v, want the pasta, and the search it came from", chosen)
	}
	log := ada.History()
	sent := log[len(log)-1]
	if sent.Text != "a pasta, then" {
		t.Errorf("message = %s, want what the result carried", sent)
	}
	if sent.FromBot {
		t.Errorf("message = %+v, want it sent by ada rather than by the bot", sent)
	}
	// It is an ordinary message once it lands, so the bot may edit it.
	if _, err := b.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID: ada.ChatID(), MessageID: sent.ID, Text: "on its way",
	}); err != nil {
		t.Errorf("edit: %v", err)
	}
}

func TestAPickedResultCarriesItsMediaAndKeyboard(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada.SendPhoto("lunch.jpg", []byte("jpeg"), "")
	k.Settle()
	held := ada.History()[0].FileID

	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery == nil {
			return
		}
		if _, err := b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
			InlineQueryID: u.InlineQuery.ID,
			Results: []models.InlineQueryResult{&models.InlineQueryResultCachedPhoto{
				ID: "lunch", PhotoFileID: held, Caption: "lunch",
				ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
					{{Text: "More", CallbackData: "more"}},
				}},
			}},
		}); err != nil {
			t.Errorf("answer: %v", err)
		}
	})

	ada.Search("lunch")
	k.Settle()
	ada.Pick("lunch")
	k.Settle()

	log := ada.History()
	sent := log[len(log)-1]
	if sent.Media != "photo" || sent.Text != "lunch" {
		t.Errorf("message = %s, want the photo the result named", sent)
	}
	if sent.FileID != held {
		t.Errorf("file = %q, want the one the bot already had (%q)", sent.FileID, held)
	}
	if f, ok := k.File(sent.FileID); !ok || string(f.Data) != "jpeg" {
		t.Errorf("file = %+v, want the bytes ada uploaded", f)
	}
	if len(sent.Keyboard) != 1 {
		t.Errorf("keyboard = %v, want the one the result carried", sent.Keyboard)
	}
}

func TestACachedResultNamesAFileTheBotHolds(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var err error
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		_, err = b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
			InlineQueryID: u.InlineQuery.ID,
			Results: []models.InlineQueryResult{
				&models.InlineQueryResultCachedPhoto{ID: "lunch", PhotoFileID: "never-issued"},
			},
		})
	})

	k.User(7).Search("lunch")
	if err == nil || !strings.Contains(err.Error(), "wrong file identifier") {
		t.Errorf("err = %v, want a result naming a file nobody issued refused", err)
	}
}

func TestAResultTelegramWouldNotTake(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var failed []error
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery == nil {
			return
		}
		for _, results := range [][]models.InlineQueryResult{
			{&models.InlineQueryResultArticle{
				ID: "", Title: "no id",
				InputMessageContent: &models.InputTextMessageContent{MessageText: "content, but nothing to name it by"},
			}},
			{&models.InlineQueryResultArticle{ID: "1", Title: "no content"}},
			{article("1", "One", "one"), article("1", "Again", "again")},
		} {
			_, err := b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
				InlineQueryID: u.InlineQuery.ID, Results: results,
			})
			failed = append(failed, err)
		}
	})
	ada := k.User(7)
	ada.Search("x")
	k.Settle()

	for i, err := range failed {
		if err == nil {
			t.Errorf("results %d were accepted, want them refused", i)
		}
	}
}

func TestAnAnswerArrivesOnceAndOnTime(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var second error
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery == nil {
			return
		}
		answer := &bot.AnswerInlineQueryParams{
			InlineQueryID: u.InlineQuery.ID, Results: []models.InlineQueryResult{article("1", "One", "one")},
		}
		if _, err := b.AnswerInlineQuery(ctx, answer); err != nil {
			t.Errorf("first answer: %v", err)
		}
		_, second = b.AnswerInlineQuery(ctx, answer)
	})
	ada := k.User(7)
	ada.Search("x")
	k.Settle()

	if second == nil {
		t.Error("the query was answered twice, want the second refused")
	}
	if _, err := b.AnswerInlineQuery(context.Background(), &bot.AnswerInlineQueryParams{
		InlineQueryID: "query-nobody-asked", Results: []models.InlineQueryResult{article("1", "One", "one")},
	}); err == nil {
		t.Error("a query nobody asked was answered, want it refused")
	}
}

func TestPickingWhatWasNotOffered(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	b := newClient(t, k)
	ada := k.User(7)

	ada.Pick("Pizza") // nothing searched for yet

	offering(t, k, b, article("pizza", "Pizza", "a pizza"))
	ada.Search("p")
	k.Settle()
	ada.Pick("Sushi")

	errs := tb.errors()
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want one for each pick that could not be made", errs)
	}
	if !strings.Contains(errs[1], "Pizza") {
		t.Errorf("error = %q, want it to say what was on offer", errs[1])
	}
}

func TestTooManyResults(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var refused error
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery == nil {
			return
		}
		many := make([]models.InlineQueryResult, mostResults+1)
		for i := range many {
			many[i] = article(strconv.Itoa(i), "One", "one")
		}
		_, refused = b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
			InlineQueryID: u.InlineQuery.ID, Results: many,
		})
	})
	k.User(7).Search("x")
	k.Settle()

	if refused == nil {
		t.Error("more results than Telegram takes were accepted, want them refused")
	}
}

func TestPickTakesTheNewestSearchPastTheNinth(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(ctx context.Context, u *models.Update) {
		if u.InlineQuery == nil {
			return
		}
		b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
			InlineQueryID: u.InlineQuery.ID,
			Results: []models.InlineQueryResult{
				article("r"+u.InlineQuery.Query, "for "+u.InlineQuery.Query, "about "+u.InlineQuery.Query),
			},
		})
	})
	ada := k.User(7)

	for i := 1; i <= 11; i++ {
		ada.Search(strconv.Itoa(i))
		k.Settle()
	}
	ada.Pick("for 11")

	sent := ada.History()
	if last := sent[len(sent)-1]; last.Text != "about 11" {
		t.Errorf("picked %q, want the eleventh search's result", last.Text)
	}
}
