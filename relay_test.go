package kitchen

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const otherChatID = testChatID + 1

func TestForwardCarriesItsOrigin(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})

	ada := k.User(testChatID, WithFullName("Ada", "Lovelace"))
	ada.Send("hello")

	forwarded, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID:     otherChatID,
		FromChatID: testChatID,
		MessageID:  ada.Screen().ID,
	})
	if err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}
	if forwarded.Text != "hello" {
		t.Errorf("forwarded = %+v, want the text it carried over", forwarded)
	}

	landed := k.History(otherChatID)
	if len(landed) != 1 || landed[0].ForwardedFrom != "Ada Lovelace" {
		t.Errorf("chat = %v, want one message attributed to its writer", landed)
	}
}

func TestForwardDropsTheKeyboard(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	menu, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID:      testChatID,
		Text:        "menu",
		ReplyMarkup: testKeyboard,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	forwarded, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID:     otherChatID,
		FromChatID: testChatID,
		MessageID:  menu.ID,
	})
	if err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}
	if forwarded.ReplyMarkup != nil {
		t.Errorf("forwarded = %+v, want a keyboard whose callbacks mean nothing here dropped", forwarded)
	}
}

func TestReForwardKeepsTheFirstSender(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})

	ada := k.User(testChatID, WithFullName("Ada", "Lovelace"))
	ada.Send("hello")

	once, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID: otherChatID, FromChatID: testChatID, MessageID: ada.Screen().ID,
	})
	if err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}
	if _, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID: otherChatID + 1, FromChatID: otherChatID, MessageID: once.ID,
	}); err != nil {
		t.Fatalf("second ForwardMessage: %v", err)
	}

	landed := k.History(otherChatID + 1)
	if len(landed) != 1 || landed[0].ForwardedFrom != "Ada Lovelace" {
		t.Errorf("chat = %v, want the first sender still named", landed)
	}
}

func TestCopyArrivesWithoutAttribution(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})

	ada := k.User(testChatID, WithFullName("Ada", "Lovelace"))
	ada.Send("hello")

	forwarded, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID: otherChatID, FromChatID: testChatID, MessageID: ada.Screen().ID,
	})
	if err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}

	copied, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: otherChatID + 1, FromChatID: otherChatID, MessageID: forwarded.ID,
	})
	if err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}

	landed := k.History(otherChatID + 1)
	if len(landed) != 1 || landed[0].ID != copied.ID {
		t.Fatalf("chat = %v, want the message the id came back for", landed)
	}
	if landed[0].Text != "hello" || landed[0].ForwardedFrom != "" {
		t.Errorf("copy = %+v, want the words without the attribution", landed[0])
	}
	if !landed[0].FromBot {
		t.Errorf("copy = %+v, want it to read as the bot's own message", landed[0])
	}
}

func TestCopyTakesTheKeyboardItIsGiven(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	menu, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: testChatID, Text: "menu", ReplyMarkup: testKeyboard,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	replacement := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "Later", CallbackData: "later"},
	}}}
	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: otherChatID, FromChatID: testChatID, MessageID: menu.ID, ReplyMarkup: replacement,
	}); err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}

	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: otherChatID, FromChatID: testChatID, MessageID: menu.ID,
	}); err != nil {
		t.Fatalf("CopyMessage without a keyboard: %v", err)
	}

	landed := k.History(otherChatID)
	if len(landed) != 2 {
		t.Fatalf("chat = %v, want both copies", landed)
	}
	if !landed[0].HasButton("Later") || landed[0].HasButton("Yes") {
		t.Errorf("copy = %v, want only the keyboard the call asked for", landed[0])
	}
	if landed[1].Keyboard != nil {
		t.Errorf("copy = %v, want a call that named no keyboard to produce none", landed[1])
	}
}

func TestCopyReplacesACaption(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	photo, err := b.SendPhoto(context.Background(), &bot.SendPhotoParams{
		ChatID:  testChatID,
		Photo:   &models.InputFileString{Data: "file-1"},
		Caption: "before",
	})
	if err != nil {
		t.Fatalf("SendPhoto: %v", err)
	}

	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: otherChatID, FromChatID: testChatID, MessageID: photo.ID, Caption: "after",
	}); err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}

	landed := k.History(otherChatID)
	if len(landed) != 1 || landed[0].Text != "after" || landed[0].Media != "photo" {
		t.Errorf("copy = %v, want the photo under its new caption", landed)
	}
}

func TestRelayingAMissingMessageIsRefused(t *testing.T) {
	k := New(t)
	for method, want := range map[string]string{
		"forwardMessage": "message to forward not found",
		"copyMessage":    "message to copy not found",
	} {
		reply := callJSON(t, k, method, `{"chat_id":1,"from_chat_id":2,"message_id":9}`)
		if reply.OK || !strings.Contains(reply.Description, want) {
			t.Errorf("%s = %+v, want %q", method, reply, want)
		}
	}
}

// A → bot → B, the whole point of a relay: what B sees, and that A's own chat
// is left alone.
func TestARelayReachesTheOtherUser(t *testing.T) {
	k := New(t)
	k.DeliverTo(syncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		b.ForwardMessage(ctx, &bot.ForwardMessageParams{
			ChatID:     otherChatID,
			FromChatID: u.Message.Chat.ID,
			MessageID:  u.Message.ID,
		})
	}).ProcessUpdate)

	ada := k.User(testChatID, WithFullName("Ada", "Lovelace"))
	bob := k.User(otherChatID, WithFullName("Bob", "Bobson"))
	ada.Send("hello")

	screen := bob.ExpectScreen(TextIs("hello"))
	if got := screen.String(); got != "(forwarded from Ada Lovelace) hello" {
		t.Errorf("Bob sees %q, want the forward marked as a client shows it", got)
	}
	if log := ada.History(); len(log) != 1 {
		t.Errorf("Ada's chat = %v, want the relay to leave it alone", log)
	}
}

func TestAForwardedPostNamesTheChannelItCameFrom(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})

	news := k.Channel(-1002, "Releases")
	post := news.Post("v1 is out")

	forwarded, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID:     testChatID,
		FromChatID: news.ID(),
		MessageID:  post.ID,
	})
	if err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}
	// Where it landed decides who sent it; the channel is only where it began.
	if forwarded.SenderChat != nil {
		t.Errorf("sender chat = %+v, want the channel left behind", forwarded.SenderChat)
	}

	landed := k.History(testChatID)
	if len(landed) != 1 || landed[0].ForwardedFrom != "Releases" {
		t.Errorf("chat = %v, want the post attributed to the channel", landed)
	}
}
