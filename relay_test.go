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
	k := talking(t)
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

func TestForwardDropsAKeyboardOfCallbacks(t *testing.T) {
	k := talking(t)
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

func TestForwardKeepsAKeyboardOfLinks(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)

	links := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "Site", URL: "https://example.com"}},
		{{Text: "Sign in", LoginURL: &models.LoginURL{URL: "https://example.com/login"}}},
		{{Text: "Code", CopyText: &models.CopyTextButton{Text: "FRIEND10"}}},
	}}
	mixed := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "Site", URL: "https://example.com"}, {Text: "Vote", CallbackData: "vote"}},
	}}
	for _, markup := range []*models.InlineKeyboardMarkup{links, mixed} {
		sent, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
			ChatID: testChatID, Text: "read more", ReplyMarkup: markup,
		})
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		if _, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
			ChatID: otherChatID, FromChatID: testChatID, MessageID: sent.ID,
		}); err != nil {
			t.Fatalf("ForwardMessage: %v", err)
		}
	}

	landed := k.History(otherChatID)
	if len(landed) != 2 {
		t.Fatalf("chat = %v, want both forwards", landed)
	}
	if got := landed[0].Buttons(); len(got) != 3 || got[1].URL != "https://example.com/login" || got[2].Label != "Code" {
		t.Errorf("forward = %+v, want the links and the copy button kept, the sign-in turned into a link", got)
	}
	if landed[1].Keyboard != nil {
		t.Errorf("forward = %+v, want one button for the bot to take the keyboard down", landed[1].Keyboard)
	}
}

func TestOnePartOfAnAlbumTravelsAlone(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})

	ada := k.User(testChatID)
	ada.SendAlbum(Photo("one.jpg", []byte("a"), ""), Photo("two.jpg", []byte("b"), ""))
	part := ada.History()[0]

	if _, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID: otherChatID, FromChatID: testChatID, MessageID: part.ID,
	}); err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}
	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: otherChatID, FromChatID: testChatID, MessageID: part.ID,
	}); err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}

	for _, m := range k.History(otherChatID) {
		if m.Album != "" {
			t.Errorf("relayed = %+v, want it out of the album it left", m)
		}
	}
}

func TestAForwardFromAGroupSpeakingAsItselfNamesTheGroup(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)

	team := k.Supergroup(-1003, "Team")
	info, _ := k.world.info(team.ID())
	said := k.world.add(team.ID(), models.Message{SenderChat: &info, AuthorSignature: "Ops", Text: "meeting moved"})

	forwarded, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID: otherChatID, FromChatID: team.ID(), MessageID: said.ID,
	})
	if err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}
	origin := forwarded.ForwardOrigin
	if origin == nil || origin.MessageOriginChat == nil || origin.MessageOriginChat.SenderChat.ID != team.ID() {
		t.Fatalf("origin = %+v, want the group as the chat it came from", origin)
	}
	if sig := origin.MessageOriginChat.AuthorSignature; sig == nil || *sig != "Ops" {
		t.Errorf("signature = %v, want the admin's signature carried", sig)
	}
	if got := k.History(otherChatID)[0].ForwardedFrom; got != "Team" {
		t.Errorf("forwarded from %q, want the group's title", got)
	}
}

func TestAForwardedPostKeepsItsSignature(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)

	news := k.Channel(-1002, "Releases")
	post := k.world.add(news.ID(), models.Message{AuthorSignature: "Ada", Text: "v1 is out"})

	forwarded, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID: otherChatID, FromChatID: news.ID(), MessageID: post.ID,
	})
	if err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}
	channel := forwarded.ForwardOrigin.MessageOriginChannel
	if channel == nil || channel.MessageID != post.ID || channel.AuthorSignature == nil || *channel.AuthorSignature != "Ada" {
		t.Errorf("origin = %+v, want the post and who signed it", forwarded.ForwardOrigin)
	}
	if forwarded.AuthorSignature != "" {
		t.Errorf("signature = %q, want it left on the origin, not on the bot's message", forwarded.AuthorSignature)
	}
}

func TestEveryOriginNamesWhereItBegan(t *testing.T) {
	cases := map[string]*models.MessageOrigin{
		"Ada Lovelace": {Type: models.MessageOriginTypeUser, MessageOriginUser: &models.MessageOriginUser{
			SenderUser: models.User{FirstName: "Ada", LastName: "Lovelace"},
		}},
		"Anonymous": {Type: models.MessageOriginTypeHiddenUser, MessageOriginHiddenUser: &models.MessageOriginHiddenUser{
			SenderUserName: "Anonymous",
		}},
		"Team": {Type: models.MessageOriginTypeChat, MessageOriginChat: &models.MessageOriginChat{
			SenderChat: models.Chat{Title: "Team"},
		}},
		"Releases": {Type: models.MessageOriginTypeChannel, MessageOriginChannel: &models.MessageOriginChannel{
			Chat: models.Chat{Title: "Releases"},
		}},
	}
	for want, origin := range cases {
		if got := forwardedFrom(origin); got != want {
			t.Errorf("forwardedFrom(%s) = %q, want %q", origin.Type, got, want)
		}
	}
}

func TestReForwardKeepsTheFirstSender(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})

	ada := k.User(testChatID, WithFullName("Ada", "Lovelace"))
	k.User(otherChatID+1, Started())
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
	k := talking(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})

	ada := k.User(testChatID, WithFullName("Ada", "Lovelace"))
	k.User(otherChatID+1, Started())
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
	k := talking(t)
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
	k := talking(t)
	b := newClient(t, k)

	photo, err := b.SendPhoto(context.Background(), &bot.SendPhotoParams{
		ChatID:  testChatID,
		Photo:   &models.InputFileString{Data: k.Upload("photo", "", nil).ID},
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
	k := talking(t)
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
	k := talking(t)
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

// A copy is a send, so it raises the hard keyboard the same way one does.
func TestACopyCarriesTheKeyboardUnderTheComposeBox(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(testChatID)
	ada.Send("hello")

	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: testChatID, FromChatID: testChatID, MessageID: ada.Screen().ID,
		ReplyMarkup: &models.ReplyKeyboardMarkup{
			Keyboard: [][]models.KeyboardButton{{{Text: "Search"}}},
		},
	}); err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}
	if !ada.HasKey("Search") {
		t.Errorf("menu = %v, want the copy to have raised it", ada.Menu())
	}
}
