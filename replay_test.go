package kitchen

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/pya-h/telebot-kitchen/capture"
)

// concierge is the bot both halves of a recording are run against: it greets,
// offers a button, and edits its own message when the button is tapped.
func concierge(t *testing.T, k *Kitchen, b *bot.Bot) bot.HandlerFunc {
	t.Helper()
	return func(ctx context.Context, _ *bot.Bot, u *models.Update) {
		switch {
		case u.Message != nil:
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: u.Message.Chat.ID, Text: "Pick a language",
				ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
					{Text: "English", CallbackData: "lang:en"},
				}}},
			})
		case u.CallbackQuery != nil:
			tapped := u.CallbackQuery.Message.Message
			b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
			b.EditMessageText(ctx, &bot.EditMessageTextParams{
				ChatID: tapped.Chat.ID, MessageID: tapped.ID, Text: "All set: " + u.CallbackQuery.Data,
			})
		}
	}
}

func record(t *testing.T, path string) {
	t.Helper()
	tape, err := capture.To(path)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	defer tape.Close()

	k := New(t, WithBotName("Concierge"))
	b := newClient(t, k)
	handle := concierge(t, k, b)
	k.DeliverToWebhook(tape.Webhook(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			var u models.Update
			json.NewDecoder(r.Body).Decode(&u)
			handle(context.Background(), b, &u)
		}),
		func(err error) { t.Errorf("capture: %v", err) },
	))

	ada := k.User(7, WithFullName("Ada", "Lovelace"))
	ada.Send("hi")
	k.Settle()
	ada.Tap("English")
	k.Settle()
}

func TestARecordedSessionPlaysBackAtTheBot(t *testing.T) {
	tape := filepath.Join(t.TempDir(), "incident.jsonl")
	record(t, tape)

	k := New(t, WithBotName("Concierge"))
	b := newClient(t, k)
	handle := concierge(t, k, b)
	k.DeliverTo(func(ctx context.Context, u *models.Update) { handle(ctx, b, u) })

	k.Replay(tape)
	k.Settle()

	// The bot does its own work again, so the chat holds what the recording put
	// back and what the replay went on to send. Both are the truth.
	history := k.User(7).History()
	if len(history) != 3 {
		t.Fatalf("history = %v, want the recording's two messages and the bot's own reply", history)
	}
	if history[0].Text != "hi" || history[0].From != "Ada Lovelace" {
		t.Errorf("first = %s, want what ada said in production", history[0])
	}
	if history[1].Text != "All set: lang:en" || !history[1].FromBot {
		t.Errorf("second = %s, want the recorded screen edited the way production edited it", history[1])
	}
	if history[2].Text != "Pick a language" || !history[2].FromBot {
		t.Errorf("third = %s, want the reply the replay drew out of the bot", history[2])
	}
}

// A tap names the screen it was made on. Without that screen back in the world
// the bot's edit would be refused and the replay would prove nothing.
func TestAReplayPutsTheConversationBackFirst(t *testing.T) {
	tape := filepath.Join(t.TempDir(), "incident.jsonl")
	record(t, tape)

	k := New(t, WithBotName("Concierge"))
	b := newClient(t, k)
	handle := concierge(t, k, b)
	k.DeliverTo(func(ctx context.Context, u *models.Update) { handle(ctx, b, u) })
	k.Replay(tape)
	k.Settle()

	edit, made := k.Calls().Last(Method("editMessageText"))
	if !made {
		t.Fatal("the bot never edited, so the tap did not reach the screen it named")
	}
	if edit.Error != "" {
		t.Errorf("edit was refused: %s; want the recorded screen back in the world to edit", edit.Error)
	}
	if edit.Params["message_id"] != "2" {
		t.Errorf("edited message %q, want the one the recording said was tapped", edit.Params["message_id"])
	}
}

func TestAReplayBringsBackTheRoomAndTheRoster(t *testing.T) {
	tape := filepath.Join(t.TempDir(), "group.jsonl")
	write(t, tape,
		`{"update_id":90,"message":{"message_id":11,"date":1700000000,"text":"hi",`+
			`"chat":{"id":-1001,"type":"supergroup","title":"Standup"},`+
			`"from":{"id":7,"is_bot":false,"first_name":"Ada","last_name":"Lovelace","username":"ada"}}}`,
	)

	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	k.Replay(tape)

	chat, err := b.GetChat(context.Background(), &bot.GetChatParams{ChatID: int64(-1001)})
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if chat.Title != "Standup" || chat.Type != models.ChatTypeSupergroup {
		t.Errorf("chat = %+v, want the supergroup the recording named", chat)
	}

	member, err := b.GetChatMember(context.Background(), &bot.GetChatMemberParams{ChatID: int64(-1001), UserID: 7})
	if err != nil {
		t.Fatalf("GetChatMember: %v", err)
	}
	if member.Type != models.ChatMemberTypeMember {
		t.Errorf("ada is %q, want her in the chat she spoke in", member.Type)
	}
	if got := member.Member.User.Username; got != "ada" {
		t.Errorf("username = %q, want the one the recording carried", got)
	}
}

// A message put back at its own id leaves the ids above it free, or the bot's
// next send lands on top of what the recording already holds.
func TestWhatTheBotSendsAfterAReplayDoesNotLandOnIt(t *testing.T) {
	tape := filepath.Join(t.TempDir(), "one.jsonl")
	write(t, tape,
		`{"update_id":1,"message":{"message_id":4100,"date":1700000000,"text":"hi",`+
			`"chat":{"id":7,"type":"private"},"from":{"id":7,"is_bot":false,"first_name":"Ada"}}}`,
	)

	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	k.Replay(tape)

	sent, err := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: int64(7), Text: "after"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if sent.ID <= 4100 {
		t.Errorf("message id = %d, want it past the one the recording restored", sent.ID)
	}
	if history := k.User(7).History(); len(history) != 2 || history[1].Text != "after" {
		t.Errorf("history = %+v, want the recorded message then the new one", history)
	}
}

func TestAReplayWithNothingToPlaySaysSo(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	defer tb.close()

	empty := filepath.Join(t.TempDir(), "empty.jsonl")
	write(t, empty, "")
	k.Replay(empty)
	k.Replay(filepath.Join(t.TempDir(), "nowhere.jsonl"))

	errs := tb.errors()
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want one for the empty recording and one for the missing file", errs)
	}
	if !strings.Contains(errs[0], "no updates") || !strings.Contains(errs[1], "nowhere.jsonl") {
		t.Errorf("errors = %v, want each to name what was wrong", errs)
	}
}

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// Somebody scrolling back to tap an older screen names a message that comes
// after a newer one on the tape. The chat still has to read in its own order.
func TestARecordingOutOfOrderIsPutBackInOrder(t *testing.T) {
	tape := filepath.Join(t.TempDir(), "scrolled.jsonl")
	write(t, tape,
		`{"update_id":1,"message":{"message_id":50,"date":1700000000,"text":"latest",`+
			`"chat":{"id":7,"type":"private"},"from":{"id":7,"is_bot":false,"first_name":"Ada"}}}`,
		`{"update_id":2,"callback_query":{"id":"q1","data":"old","chat_instance":"c",`+
			`"from":{"id":7,"is_bot":false,"first_name":"Ada"},`+
			`"message":{"message_id":20,"date":1699000000,"text":"an older screen",`+
			`"chat":{"id":7,"type":"private"},"from":{"id":900,"is_bot":true,"first_name":"Bot"}}}}`,
	)

	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	k.Replay(tape)

	ada := k.User(7)
	var order []int
	for _, m := range ada.History() {
		order = append(order, m.ID)
	}
	if len(order) != 2 || order[0] != 20 || order[1] != 50 {
		t.Errorf("history ids = %v, want them in the chat's own order", order)
	}
	if newest := ada.Screen().Text; newest != "latest" {
		t.Errorf("screen = %q, want the newest message rather than the last one restored", newest)
	}
}

func TestAMessageTheRecordingEditedEndsWhereItEnded(t *testing.T) {
	tape := filepath.Join(t.TempDir(), "edited.jsonl")
	write(t, tape,
		`{"update_id":1,"message":{"message_id":5,"date":1700000000,"text":"hello",`+
			`"chat":{"id":7,"type":"private"},"from":{"id":7,"is_bot":false,"first_name":"Ada"}}}`,
		`{"update_id":2,"edited_message":{"message_id":5,"date":1700000000,"edit_date":1700000009,`+
			`"text":"goodbye","chat":{"id":7,"type":"private"},"from":{"id":7,"is_bot":false,"first_name":"Ada"}}}`,
	)

	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	k.Replay(tape)

	ada := k.User(7)
	if got := ada.Screen().Text; got != "goodbye" {
		t.Errorf("message reads %q, want what the recording left it as", got)
	}
	if history := ada.History(); len(history) != 1 {
		t.Errorf("history = %v, want the one message rather than both of its versions", history)
	}
}
