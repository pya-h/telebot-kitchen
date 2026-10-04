package kitchen

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// heard is every message the bot is handed, in the order it was handed them.
func heard(k *Kitchen) *[]models.Message {
	var got []models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) {
		if u.Message != nil {
			got = append(got, *u.Message)
		}
	})
	return &got
}

func spans(text string, entities []models.MessageEntity) []string {
	var seen []string
	for _, one := range entitiesOf(text, entities) {
		seen = append(seen, one.Kind+":"+one.Text)
	}
	return seen
}

func TestAMemberFormatsTheWayAClientDoes(t *testing.T) {
	k := New(t)
	got := heard(k)
	ada := k.User(7)

	ada.SendFormatted(HTML("سلام <b>دوست</b> 😀 <i>خوبی؟</i>"))
	ada.SendFormatted(MarkdownV2("😀 *bold* _italic_ __under__ ~gone~ ||secret|| `code`"))

	if len(*got) != 2 {
		t.Fatalf("the bot heard %d messages, want 2", len(*got))
	}
	first := (*got)[0]
	if first.Text != "سلام دوست 😀 خوبی؟" {
		t.Errorf("text = %q, want the markup off", first.Text)
	}
	if want := []models.MessageEntity{
		{Type: models.MessageEntityTypeBold, Offset: 5, Length: 4},
		{Type: models.MessageEntityTypeItalic, Offset: 13, Length: 5},
	}; !slices.Equal(first.Entities, want) {
		t.Errorf("entities = %+v, want %+v, counted in UTF-16 past the emoji", first.Entities, want)
	}
	second := (*got)[1]
	want := []string{"bold:bold", "italic:italic", "underline:under", "strikethrough:gone", "spoiler:secret", "code:code"}
	if got := spans(second.Text, second.Entities); !slices.Equal(got, want) {
		t.Errorf("spans = %v, want %v", got, want)
	}
	if second.Entities[0].Offset != 3 {
		t.Errorf("bold starts at %d, want 3, the emoji counting twice", second.Entities[0].Offset)
	}
}

func TestAMemberGivesSpansOutright(t *testing.T) {
	k := New(t)
	got := heard(k)
	ada := k.User(7)
	bob := k.User(8, WithFullName("Bob", "Bobson"))

	ada.SendFormatted(Plain("see this, ask Bob 🙂 then\nrun it",
		Span{Kind: "text_link", Offset: 0, Length: 8, URL: "https://example.com"},
		Span{Kind: "text_mention", Offset: 14, Length: 3, User: bob},
		Span{Kind: "custom_emoji", Offset: 18, Length: 2, CustomEmoji: "5368324170671202286"},
		Span{Kind: "pre", Offset: 26, Length: 6, Language: "sh"},
		Span{Kind: "expandable_blockquote", Offset: 0, Length: 25},
	))

	if len(*got) != 1 {
		t.Fatalf("the bot heard %d messages, want 1", len(*got))
	}
	entities := (*got)[0].Entities
	if len(entities) != 5 || entities[0].Type != models.MessageEntityTypeExpandableBlockquote {
		t.Fatalf("entities = %+v, want all five, outermost first", entities)
	}
	byKind := map[models.MessageEntityType]models.MessageEntity{}
	for _, e := range entities {
		byKind[e.Type] = e
	}
	if link := byKind[models.MessageEntityTypeTextLink]; link.URL != "https://example.com" {
		t.Errorf("text_link = %+v, want its URL", link)
	}
	if mention := byKind[models.MessageEntityTypeTextMention]; mention.User == nil || mention.User.ID != 8 || mention.User.FirstName != "Bob" {
		t.Errorf("text_mention = %+v, want Bob as Telegram describes him", mention)
	}
	if emoji := byKind[models.MessageEntityTypeCustomEmoji]; emoji.CustomEmojiID != "5368324170671202286" {
		t.Errorf("custom_emoji = %+v, want its id", emoji)
	}
	if pre := byKind[models.MessageEntityTypePre]; pre.Language != "sh" {
		t.Errorf("pre = %+v, want its language", pre)
	}
	if got := ada.Screen().Entities[1]; got.Kind != "text_link" || got.Text != "see this" {
		t.Errorf("screen entity = %+v, want the link over the words it hides", got)
	}
}

func TestAFormattedCommandIsStillACommand(t *testing.T) {
	k := New(t)
	got := heard(k)

	k.User(7).SendFormatted(HTML("/start <b>now</b>"))
	k.User(7).SendFormatted(HTML("<i>/start now</i>"))

	if spans := spans((*got)[0].Text, (*got)[0].Entities); !slices.Equal(spans, []string{"bot_command:/start", "bold:now"}) {
		t.Errorf("spans = %v, want the command marked along with the bold", spans)
	}
	if spans := spans((*got)[1].Text, (*got)[1].Entities); !slices.Equal(spans, []string{"italic:/start now", "bot_command:/start"}) {
		t.Errorf("spans = %v, want the italic around the command, outermost first", spans)
	}
}

func TestAMemberCaptionsAFileWithFormatting(t *testing.T) {
	k := New(t)
	got := heard(k)
	ada := k.User(7)

	ada.SendFile(Photo("lunch.jpg", []byte("a"), "").Captioned(MarkdownV2("lunch at *Rossi*")))
	ada.SendFile(Voice("hi.ogg", []byte("b"), "").Captioned(Plain("hi 😀", Span{Kind: "spoiler", Offset: 3, Length: 2})))
	ada.SendFile(Animation("cat.gif", []byte("c"), "plain"))
	ada.SendAlbum(
		Photo("one.jpg", []byte("d"), "").Captioned(HTML("<u>us</u>")),
		Video("two.mp4", []byte("e"), "then"),
	)

	if len(*got) != 5 {
		t.Fatalf("the bot heard %d messages, want 5", len(*got))
	}
	want := [][]string{{"bold:Rossi"}, {"spoiler:😀"}, nil, {"underline:us"}, nil}
	for i, m := range *got {
		if spans := spans(m.Caption, m.CaptionEntities); !slices.Equal(spans, want[i]) {
			t.Errorf("caption %d %q spans = %v, want %v", i, m.Caption, spans, want[i])
		}
	}
	if (*got)[1].Voice == nil || (*got)[2].Animation == nil || (*got)[2].Caption != "plain" {
		t.Errorf("messages = %+v, want a voice and a captioned animation", (*got)[1:3])
	}
	if (*got)[3].MediaGroupID == "" || (*got)[3].MediaGroupID != (*got)[4].MediaGroupID {
		t.Errorf("album = %q %q, want one group", (*got)[3].MediaGroupID, (*got)[4].MediaGroupID)
	}
	if got := ada.History()[0].String(); got != "(photo lunch.jpg) lunch at **Rossi**" {
		t.Errorf("screen = %q, want the caption shown styled", got)
	}
}

func TestACopyOfFormattedTextKeepsOnlyWhatTheBotGives(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(testChatID)
	bob := k.User(otherChatID)

	ada.SendFormatted(HTML(`<b>hi</b> <a href="https://example.com">there</a>`))
	ada.SendFile(Photo("a.jpg", []byte("a"), "").Captioned(HTML(`<a href="https://example.com">look</a>`)))
	text, photo := ada.History()[0], ada.History()[1]

	for _, id := range []int{text.ID, photo.ID} {
		if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
			ChatID: bob.ID(), FromChatID: ada.ID(), MessageID: id,
		}); err != nil {
			t.Fatalf("CopyMessage: %v", err)
		}
	}
	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: bob.ID(), FromChatID: ada.ID(), MessageID: photo.ID,
		Caption:         "look closer",
		CaptionEntities: []models.MessageEntity{{Type: models.MessageEntityTypeItalic, Offset: 5, Length: 6}},
	}); err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}

	landed := bob.History()
	want := [][]string{
		{"bold:hi", "text_link:there(https://example.com)"},
		{"text_link:look(https://example.com)"},
		{"italic:closer"},
	}
	if len(landed) != len(want) {
		t.Fatalf("Bob's chat = %v, want %d copies", landed, len(want))
	}
	for i, m := range landed {
		if got := kinds(m.Entities); !slices.Equal(got, want[i]) {
			t.Errorf("copy %d entities = %v, want %v", i, got, want[i])
		}
	}
}

func TestAFormattingMistakeIsTheTestsOwn(t *testing.T) {
	cases := map[string]func(*Member){
		`does not read as HTML`:      func(m *Member) { m.SendFormatted(HTML("<b>open")) },
		`no "boldest" span`:          func(m *Member) { m.SendFormatted(Plain("hi", Span{Kind: "boldest", Length: 2})) },
		`text_link span needs a URL`: func(m *Member) { m.SendFormatted(Plain("hi", Span{Kind: "text_link", Length: 2})) },
		`text_mention span needs a user`: func(m *Member) {
			m.SendFormatted(Plain("hi", Span{Kind: "text_mention", Length: 2}))
		},
		`does not lie inside "hi"`:     func(m *Member) { m.SendFormatted(Plain("hi", Span{Kind: "bold", Offset: 1, Length: 2})) },
		`splits a character`:           func(m *Member) { m.SendFormatted(Plain("😀", Span{Kind: "bold", Length: 1})) },
		`cannot send an empty message`: func(m *Member) { m.SendFormatted(Plain("")) },
		`cannot send that caption`: func(m *Member) {
			m.SendFile(Photo("a.jpg", nil, "").Captioned(Plain("hi", Span{Kind: "bold", Length: 3})))
		},
		`cannot send that album`: func(m *Member) {
			m.SendAlbum(Photo("a.jpg", nil, "ok"), Photo("b.jpg", nil, "").Captioned(MarkdownV2("*open")))
		},
	}
	for want, send := range cases {
		t.Run(want, func(t *testing.T) {
			tb := &recordingTB{}
			defer tb.close()
			k := New(tb)
			got := heard(k)

			send(k.User(7).Member)

			if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], want) {
				t.Errorf("errors = %v, want one saying %q", errs, want)
			}
			if len(*got) != 0 {
				t.Errorf("the bot heard %v, want nothing", *got)
			}
		})
	}
}
