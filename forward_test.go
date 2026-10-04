package kitchen

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestEveryKindOfOriginReachesTheBot(t *testing.T) {
	k := talking(t)
	got := heard(k)
	k.Clock().Advance(3600e9)
	now := int(k.Clock().Now().Unix())

	ada := k.User(testChatID, WithFullName("Ada", "Lovelace"))
	carl := k.User(9, WithFullName("Carl", "Gauss"))
	team := k.Supergroup(-1003, "Team")
	news := k.Channel(-1002, "Releases")

	ada.ForwardText(FromUser(carl), Plain("from a person"))
	ada.ForwardText(FromHiddenUser("Someone Private"), Plain("from a hidden person"))
	ada.ForwardText(FromChat(team, "Ops"), Plain("from a group"))
	ada.ForwardText(FromChannel(news, 42, "Ada"), Plain("from a channel"))

	if len(*got) != 4 {
		t.Fatalf("the bot heard %d messages, want 4", len(*got))
	}
	for _, m := range *got {
		if m.From == nil || m.From.ID != ada.ID() || m.Chat.ID != ada.ID() || m.Date != now {
			t.Errorf("message = %+v, want it from Ada, in her chat, sent now", m)
		}
		if m.ForwardOrigin == nil {
			t.Fatalf("message %q carries no origin", m.Text)
		}
	}

	user := (*got)[0].ForwardOrigin.MessageOriginUser
	if user == nil || user.SenderUser.ID != 9 || user.SenderUser.FirstName != "Carl" || user.Date != now {
		t.Errorf("user origin = %+v, want Carl", (*got)[0].ForwardOrigin)
	}
	hidden := (*got)[1].ForwardOrigin.MessageOriginHiddenUser
	if hidden == nil || hidden.SenderUserName != "Someone Private" {
		t.Errorf("hidden origin = %+v, want the name alone", (*got)[1].ForwardOrigin)
	}
	chat := (*got)[2].ForwardOrigin.MessageOriginChat
	if chat == nil || chat.SenderChat.ID != team.ID() || chat.SenderChat.Type != models.ChatTypeSupergroup ||
		chat.AuthorSignature == nil || *chat.AuthorSignature != "Ops" {
		t.Errorf("chat origin = %+v, want the group and its admin's signature", (*got)[2].ForwardOrigin)
	}
	channel := (*got)[3].ForwardOrigin.MessageOriginChannel
	if channel == nil || channel.Chat.ID != news.ID() || channel.MessageID != 42 ||
		channel.AuthorSignature == nil || *channel.AuthorSignature != "Ada" {
		t.Errorf("channel origin = %+v, want the post and who signed it", (*got)[3].ForwardOrigin)
	}

	var shown []string
	for _, m := range ada.History() {
		shown = append(shown, m.ForwardedFrom)
	}
	if want := []string{"Carl Gauss", "Someone Private", "Team", "Releases"}; !slices.Equal(shown, want) {
		t.Errorf("forwarded from %v, want %v", shown, want)
	}
}

func TestAMemberForwardsWhatIsAlreadyThere(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	got := heard(k)

	ada := k.User(testChatID)
	carl := k.User(9, WithFullName("Carl", "Gauss"))
	team := k.Group(-1003, "Team")
	news := k.Channel(-1002, "Releases")

	post := news.Post("v1 is out")
	carl.In(team).Send("lunch?")
	said := team.History()[0]
	reply, err := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: ada.ID(), Text: "welcome"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	*got = nil

	ada.Forward(post, said, ada.History()[0])

	if len(*got) != 3 {
		t.Fatalf("the bot heard %d messages, want 3", len(*got))
	}
	channel := (*got)[0].ForwardOrigin.MessageOriginChannel
	if channel == nil || channel.Chat.ID != news.ID() || channel.MessageID != post.ID {
		t.Errorf("origin = %+v, want the post it was", (*got)[0].ForwardOrigin)
	}
	if user := (*got)[1].ForwardOrigin.MessageOriginUser; user == nil || user.SenderUser.ID != carl.ID() || user.Date != int(said.Sent.Unix()) {
		t.Errorf("origin = %+v, want Carl, dated when he said it", (*got)[1].ForwardOrigin)
	}
	if user := (*got)[2].ForwardOrigin.MessageOriginUser; user == nil || user.SenderUser.ID != reply.From.ID {
		t.Errorf("origin = %+v, want the bot that wrote it", (*got)[2].ForwardOrigin)
	}
	for _, m := range *got {
		if m.From.ID != ada.ID() || m.SenderChat != nil {
			t.Errorf("message = %+v, want Ada as the one who sent it", m)
		}
	}
}

func TestAUserWhoHidesIsForwardedByNameAlone(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	got := heard(k)

	ada := k.User(testChatID)
	carl := k.User(9, WithFullName("Carl", "Gauss"), HidesForwards(), Started())
	carl.Send("keep me out of it")

	ada.ForwardText(FromUser(carl), Plain("hello"))
	if _, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID: ada.ID(), FromChatID: carl.ID(), MessageID: carl.History()[0].ID,
	}); err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}

	if hidden := (*got)[1].ForwardOrigin.MessageOriginHiddenUser; hidden == nil || hidden.SenderUserName != "Carl Gauss" {
		t.Errorf("origin = %+v, want Carl's name and nothing to link", (*got)[1].ForwardOrigin)
	}
	if forwarded := ada.History()[1]; forwarded.ForwardedFrom != "Carl Gauss" {
		t.Errorf("forwarded from %q, want the bot's forward to hide him too", forwarded.ForwardedFrom)
	}
	k.User(9)
	if !k.hidesForwards(9) {
		t.Errorf("mentioning Carl again forgot the setting")
	}
}

func TestAForwardKeepsItsFormatting(t *testing.T) {
	k := talking(t)
	got := heard(k)
	ada := k.User(testChatID)

	ada.ForwardText(FromHiddenUser("Anon"), HTML(`<b>sale</b> at <a href="https://shop.example">our shop</a>`))
	ada.ForwardFile(FromHiddenUser("Anon"), Photo("p.jpg", []byte("a"), "").Captioned(MarkdownV2("_new_ 😀 *stock*")))
	ada.SendFormatted(HTML("<u>mine</u>"))
	ada.Forward(ada.History()[2])

	want := [][]string{
		{"bold:sale", "text_link:our shop"},
		{"italic:new", "bold:stock"},
		{"underline:mine"},
		{"underline:mine"},
	}
	for i, m := range *got {
		text, entities := m.Text, m.Entities
		if text == "" {
			text, entities = m.Caption, m.CaptionEntities
		}
		if got := spans(text, entities); !slices.Equal(got, want[i]) {
			t.Errorf("message %d spans = %v, want %v", i, got, want[i])
		}
	}
	if (*got)[3].ForwardOrigin.MessageOriginUser == nil {
		t.Errorf("origin = %+v, want Ada's own message credited to her", (*got)[3].ForwardOrigin)
	}
}

// What mmbot's relay does with a forward: copy it on, so the other side sees
// the bot's message and nobody's name.
func TestAForwardCopiedOnArrivesUncredited(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(testChatID)
	bob := k.User(otherChatID)
	carl := k.User(9, WithFullName("Carl", "Gauss"))

	ada.ForwardFile(FromUser(carl), Photo("p.jpg", []byte("a"), "").Captioned(HTML("<b>look</b>")))
	forwarded := ada.Screen()

	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: bob.ID(), FromChatID: ada.ID(), MessageID: forwarded.ID,
		Caption:         "Forwarded by Ada\nlook",
		CaptionEntities: []models.MessageEntity{{Type: models.MessageEntityTypeItalic, Offset: 0, Length: 16}},
		ReplyMarkup:     &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}},
	}); err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}
	if _, err := b.ForwardMessage(context.Background(), &bot.ForwardMessageParams{
		ChatID: bob.ID(), FromChatID: ada.ID(), MessageID: forwarded.ID,
	}); err != nil {
		t.Fatalf("ForwardMessage: %v", err)
	}

	copied, again := bob.History()[0], bob.History()[1]
	if copied.ForwardedFrom != "" || !copied.FromBot || copied.Text != "Forwarded by Ada\nlook" {
		t.Errorf("copy = %+v, want the bot's own photo under the new caption", copied)
	}
	if got := kinds(copied.Entities); !slices.Equal(got, []string{"italic:Forwarded by Ada"}) {
		t.Errorf("copy entities = %v, want only what the bot gave", got)
	}
	if copied.FileID != forwarded.FileID {
		t.Errorf("copy file = %q, want the forwarded photo's %q", copied.FileID, forwarded.FileID)
	}
	if again.ForwardedFrom != "Carl Gauss" {
		t.Errorf("forward forwarded from %q, want Carl still credited", again.ForwardedFrom)
	}
}

func TestAForwardedPostKeepsItsLinks(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	got := heard(k)
	ada := k.User(testChatID)
	news := k.Channel(-1002, "Releases")

	for _, markup := range []*models.InlineKeyboardMarkup{
		{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Read", URL: "https://example.com"}}}},
		{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Like", CallbackData: "like"}}}},
	} {
		if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
			ChatID: news.ID(), Text: "v1 is out", ReplyMarkup: markup,
		}); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}
	ada.Forward(news.History()...)

	if keyboard := (*got)[0].ReplyMarkup; keyboard == nil || keyboard.InlineKeyboard[0][0].URL != "https://example.com" {
		t.Errorf("keyboard = %+v, want the link kept", keyboard)
	}
	if keyboard := (*got)[1].ReplyMarkup; keyboard != nil {
		t.Errorf("keyboard = %+v, want a callback to take it down", keyboard)
	}
}

func TestAForwardedAlbumIsAnAlbumOfItsOwn(t *testing.T) {
	k := talking(t)
	got := heard(k)
	ada := k.User(testChatID)
	carl := k.User(9)

	team := k.Group(-1003, "Team")
	carl.In(team).SendAlbum(Photo("1.jpg", []byte("a"), "trip"), Photo("2.jpg", []byte("b"), ""), Photo("3.jpg", []byte("c"), ""))
	trip := team.History()
	*got = nil

	ada.Forward(trip[0], trip[1])
	ada.Forward(trip[2])
	ada.ForwardAlbum(FromUser(carl), Video("a.mp4", []byte("d"), ""), Photo("b.jpg", []byte("e"), ""))

	if len(*got) != 5 {
		t.Fatalf("the bot heard %d messages, want 5", len(*got))
	}
	pair, lone, built := (*got)[0:2], (*got)[2], (*got)[3:5]
	if pair[0].MediaGroupID == "" || pair[0].MediaGroupID != pair[1].MediaGroupID || pair[0].MediaGroupID == trip[0].Album {
		t.Errorf("albums = %q %q, want a new one shared, not %q", pair[0].MediaGroupID, pair[1].MediaGroupID, trip[0].Album)
	}
	if lone.MediaGroupID != "" {
		t.Errorf("lone part album = %q, want none", lone.MediaGroupID)
	}
	if built[0].MediaGroupID == "" || built[0].MediaGroupID != built[1].MediaGroupID || built[0].MediaGroupID == pair[0].MediaGroupID {
		t.Errorf("built album = %q %q, want one of its own", built[0].MediaGroupID, built[1].MediaGroupID)
	}
	for _, m := range *got {
		if m.ForwardOrigin == nil || m.ForwardOrigin.MessageOriginUser.SenderUser.ID != carl.ID() {
			t.Errorf("part = %+v, want every part credited to Carl", m)
		}
	}
	if pair[0].Caption != "trip" {
		t.Errorf("caption = %q, want the album's caption carried", pair[0].Caption)
	}
}

func TestEveryKindOfContentForwards(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	got := heard(k)
	ada := k.User(testChatID)
	from := FromHiddenUser("Anon")

	for _, file := range []Attachment{
		Photo("p.jpg", []byte("a"), "photo"), Video("v.mp4", []byte("b"), "video"),
		Animation("a.gif", []byte("c"), "animation"), Audio("a.mp3", []byte("d"), "audio"),
		Document("d.pdf", []byte("e"), "document"), Voice("v.ogg", []byte("f"), "voice"),
		Sticker("s.webp", []byte("g")), VideoNote("n.mp4", []byte("h")),
	} {
		ada.ForwardFile(from, file)
	}
	ada.ForwardLocation(from, 35.7, 51.4)
	ada.ForwardVenue(from, 35.7, 51.4, "Cafe", "Main St")

	var shown []string
	for _, m := range ada.History() {
		shown = append(shown, m.Media)
	}
	want := []string{"photo", "video", "animation", "audio", "document", "voice", "sticker", "video note", "location", "venue"}
	if !slices.Equal(shown, want) {
		t.Errorf("forwarded %v, want %v", shown, want)
	}
	for _, m := range *got {
		if m.ForwardOrigin == nil {
			t.Errorf("message = %+v, want it forwarded", m)
		}
	}
	if _, err := b.SendSticker(context.Background(), &bot.SendStickerParams{
		ChatID: otherChatID, Sticker: &models.InputFileString{Data: (*got)[6].Sticker.FileID},
	}); err != nil {
		t.Errorf("SendSticker with the forwarded sticker: %v", err)
	}
}

func TestAForwardMistakeIsTheTestsOwn(t *testing.T) {
	cases := map[string]func(*Kitchen, *Member){
		`credited to nobody`: func(k *Kitchen, m *Member) { m.ForwardText(Origin{}, Plain("hi")) },
		`forwarded FromChannel`: func(k *Kitchen, m *Member) {
			m.ForwardText(FromChat(k.Channel(-1002, "Releases"), ""), Plain("hi"))
		},
		`only a channel posts`: func(k *Kitchen, m *Member) {
			m.ForwardText(FromChannel(k.Group(-1003, "Team"), 1, ""), Plain("hi"))
		},
		`needs its message id`: func(k *Kitchen, m *Member) {
			m.ForwardText(FromChannel(k.Channel(-1002, "Releases"), 0, ""), Plain("hi"))
		},
		`which is not there`: func(k *Kitchen, m *Member) { m.Forward(Message{ChatID: -1002, ID: 5}) },
		`somebody else's private chat`: func(k *Kitchen, m *Member) {
			bob := k.User(8)
			bob.Send("private")
			m.Forward(bob.History()[0])
		},
		`a service message`: func(k *Kitchen, m *Member) {
			team := k.Group(-1003, "Team")
			k.User(8).In(team).Join()
			m.Forward(team.History()[0])
		},
		`cannot forward a poll`: func(k *Kitchen, m *Member) {
			m.SendPoll("lunch?", "yes", "no")
			m.Forward(m.History()[0])
		},
		`cannot caption a sticker`: func(k *Kitchen, m *Member) {
			m.ForwardFile(FromHiddenUser("Anon"), Sticker("s.webp", nil).Captioned(Plain("hi")))
		},
		`nothing to forward`: func(k *Kitchen, m *Member) { m.Forward() },
		`empty message`:      func(k *Kitchen, m *Member) { m.ForwardText(FromHiddenUser("Anon"), Plain("")) },
	}
	for want, forward := range cases {
		t.Run(want, func(t *testing.T) {
			tb := &recordingTB{}
			defer tb.close()
			k := New(tb)
			ada := k.User(7)
			got := heard(k)
			forward(k, ada.Member)

			if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], want) {
				t.Errorf("errors = %v, want one saying %q", errs, want)
			}
			for _, m := range *got {
				if m.ForwardOrigin != nil {
					t.Errorf("the bot heard %+v, want no forward", m)
				}
			}
		})
	}
}

// The library writes into an origin as it encodes one, so no two messages may
// share the one they point at.
func TestNoTwoForwardsShareAnOrigin(t *testing.T) {
	k := talking(t)
	got := heard(k)
	ada := k.User(testChatID)

	ada.ForwardAlbum(FromHiddenUser("Anon"), Photo("1.jpg", nil, ""), Photo("2.jpg", nil, ""))
	if (*got)[0].ForwardOrigin.MessageOriginHiddenUser == (*got)[1].ForwardOrigin.MessageOriginHiddenUser {
		t.Errorf("the two parts share one origin")
	}

	stored := ada.History()[0]
	once, _ := k.world.message(stored.ChatID, stored.ID)
	twice, _ := k.world.message(stored.ChatID, stored.ID)
	if once.ForwardOrigin.MessageOriginHiddenUser == twice.ForwardOrigin.MessageOriginHiddenUser {
		t.Errorf("two reads of one message share its origin")
	}
}
