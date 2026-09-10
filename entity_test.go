package kitchen

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func kinds(entities []Entity) []string {
	var seen []string
	for _, e := range entities {
		one := e.Kind + ":" + e.Text
		if e.URL != "" {
			one += "(" + e.URL + ")"
		}
		seen = append(seen, one)
	}
	return seen
}

func TestTheMarkupComesOffTheTextATestAssertsOn(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)

	sent, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID:    ada.ID(),
		Text:      `Welcome, *Ada*\! Your ticket is ` + "`T-4417`" + `\.`,
		ParseMode: models.ParseModeMarkdown,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// What the bot is handed back is what Telegram hands back: the plain reading.
	if sent.Text != "Welcome, Ada! Your ticket is T-4417." {
		t.Errorf("text = %q, want the markup taken off", sent.Text)
	}
	if screen := ada.Screen(); screen.Text != "Welcome, Ada! Your ticket is T-4417." {
		t.Errorf("screen = %q, want the words rather than the asterisks", screen.Text)
	}
}

func TestTheSpansAreThereToAssertOn(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)

	b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID:    ada.ID(),
		Text:      "Welcome, *Ada*\\! See [the rules](https://t.me/rules) and quote `T-4417`\\.",
		ParseMode: models.ParseModeMarkdown,
	})

	got := kinds(ada.Screen().Entities)
	want := []string{"bold:Ada", "text_link:the rules(https://t.me/rules)", "code:T-4417"}
	if !slices.Equal(got, want) {
		t.Errorf("entities = %v, want %v", got, want)
	}
}

func TestATranscriptShowsTheMarkupAClientShows(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)

	b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID:    ada.ID(),
		Text:      "Welcome, *Ada*\\! See [the rules](https://t.me/rules)\\.",
		ParseMode: models.ParseModeMarkdown,
	})

	want := "**Kitchen:** Welcome, **Ada**! See [the rules](https://t.me/rules).\n"
	if got := ada.Transcript(); got != want {
		t.Errorf("transcript =\n%q\nwant\n%q", got, want)
	}
}

// The three ways a bot may spell the same message are the same message.
func TestEveryParseModeReadsTheSameWay(t *testing.T) {
	for _, c := range []struct{ mode, text string }{
		{"MarkdownV2", "a *bold* and a [link](https://t.me/x)"},
		{"Markdown", "a *bold* and a [link](https://t.me/x)"},
		{"HTML", `a <b>bold</b> and a <a href="https://t.me/x">link</a>`},
	} {
		text, entities, err := styleOf(c.text, c.mode)
		if err != nil {
			t.Fatalf("%s: %v", c.mode, err)
		}
		if text != "a bold and a link" {
			t.Errorf("%s text = %q, want the plain reading", c.mode, text)
		}
		got := kinds(entitiesOf(text, entities))
		if want := []string{"bold:bold", "text_link:link(https://t.me/x)"}; !slices.Equal(got, want) {
			t.Errorf("%s entities = %v, want %v", c.mode, got, want)
		}
	}
}

// Telegram measures a span in UTF-16 code units, so an emoji before it counts
// twice and a Persian word inside it counts once a letter.
func TestSpansAreMeasuredInUTF16(t *testing.T) {
	text, entities, err := styleOf("🎉 *سلام* 🎉", "MarkdownV2")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if text != "🎉 سلام 🎉" {
		t.Fatalf("text = %q", text)
	}
	if len(entities) != 1 || entities[0].Offset != 3 || entities[0].Length != 4 {
		t.Errorf("entity = %+v, want offset 3 length 4", entities)
	}
	// Which is the test that matters: the offsets have to find the word again.
	if got := entitiesOf(text, entities); got[0].Text != "سلام" {
		t.Errorf("span covers %q, want the Persian word", got[0].Text)
	}
}

func TestMarkupTheKitchenCannotReadIsRefused(t *testing.T) {
	for _, c := range []struct{ mode, text, want string }{
		{"MarkdownV2", "*unclosed", "can't find end of bold entity"},
		{"MarkdownV2", "*a _b* c_", "can't find end of italic entity"},
		{"MarkdownV2", "```unfenced", "can't find end of pre entity"},
		{"HTML", "<marquee>x</marquee>", `unsupported start tag "marquee"`},
		{"HTML", "<b>x", "can't find end of bold entity"},
		{"HTML", "x</b>", `unexpected end tag "b"`},
		{"Klingon", "x", `unsupported parse mode "Klingon"`},
	} {
		_, _, err := styleOf(c.text, c.mode)
		if err == nil {
			t.Errorf("%s %q was accepted, want it refused", c.mode, c.text)
			continue
		}
		if got := err.Error(); !strings.Contains(got, c.want) {
			t.Errorf("%s %q = %q, want it to name %q", c.mode, c.text, got, c.want)
		}
	}
}

// A bot that describes its own spans is telling Telegram not to guess.
func TestEntitiesGivenOutrightWinOverAParseMode(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)

	b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID:    ada.ID(),
		Text:      "*not* markup",
		ParseMode: models.ParseModeMarkdown,
		Entities:  []models.MessageEntity{{Type: models.MessageEntityTypeCode, Offset: 0, Length: 5}},
	})

	screen := ada.Screen()
	if screen.Text != "*not* markup" {
		t.Errorf("text = %q, want it left exactly as the bot spelled it", screen.Text)
	}
	if got := kinds(screen.Entities); !slices.Equal(got, []string{"code:*not*"}) {
		t.Errorf("entities = %v, want the one the bot described", got)
	}
}

func TestACaptionIsStyledLikeText(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)

	_, err := b.SendPhoto(context.Background(), &bot.SendPhotoParams{
		ChatID:    ada.ID(),
		Photo:     &models.InputFileString{Data: "photo-id"},
		Caption:   "lunch at *Rossi*",
		ParseMode: models.ParseModeMarkdown,
	})
	if err != nil {
		t.Fatalf("SendPhoto: %v", err)
	}

	screen := ada.Screen()
	if screen.Text != "lunch at Rossi" {
		t.Errorf("caption = %q, want the markup taken off", screen.Text)
	}
	if got := kinds(screen.Entities); !slices.Equal(got, []string{"bold:Rossi"}) {
		t.Errorf("entities = %v, want the bold restaurant", got)
	}
	if want := "(photo) lunch at **Rossi**"; screen.String() != want {
		t.Errorf("screen = %q, want %q", screen.String(), want)
	}
}

// An edit that only changes the markup still changes the message.
func TestAnEditThatOnlyRestylesIsStillAnEdit(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)

	sent, _ := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: ada.ID(), Text: "steady"})
	edited, err := b.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID: ada.ID(), MessageID: sent.ID, Text: "*steady*", ParseMode: models.ParseModeMarkdown,
	})
	if err != nil {
		t.Fatalf("EditMessageText: %v", err)
	}
	if edited.Text != "steady" || len(edited.Entities) != 1 {
		t.Errorf("edited = %q %+v, want the same word marked up", edited.Text, edited.Entities)
	}
	if got := ada.Screen().String(); got != "**steady**" {
		t.Errorf("screen = %q, want the bold reading", got)
	}
}

func TestASpanThatIsNotThereIsIgnored(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)

	b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: ada.ID(),
		Text:   "short",
		Entities: []models.MessageEntity{
			{Type: models.MessageEntityTypeBold, Offset: 0, Length: 99},
			{Type: models.MessageEntityTypeCode, Offset: -1, Length: 2},
			{Type: models.MessageEntityTypeItalic, Offset: 0, Length: 5},
		},
	})

	screen := ada.Screen()
	if screen.Text != "short" {
		t.Errorf("text = %q, want it left alone", screen.Text)
	}
	if got := kinds(screen.Entities); !slices.Equal(got, []string{"italic:short"}) {
		t.Errorf("entities = %v, want only the span that is really there", got)
	}
	if got := screen.String(); got != "_short_" {
		t.Errorf("screen = %q, want only the span that is really there", got)
	}
}

func TestEachCaptionInAnAlbumIsReadOnItsOwn(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ada := k.User(7)

	_, err := b.SendMediaGroup(context.Background(), &bot.SendMediaGroupParams{
		ChatID: ada.ID(),
		Media: []models.InputMedia{
			&models.InputMediaPhoto{Media: "one", Caption: "lunch at *Rossi*", ParseMode: models.ParseModeMarkdown},
			&models.InputMediaPhoto{Media: "two", Caption: "and <i>pudding</i>", ParseMode: models.ParseModeHTML},
		},
	})
	if err != nil {
		t.Fatalf("SendMediaGroup: %v", err)
	}

	sent := ada.History()
	if len(sent) != 2 {
		t.Fatalf("history = %d messages, want the album", len(sent))
	}
	if sent[0].Text != "lunch at Rossi" || sent[1].Text != "and pudding" {
		t.Errorf("captions = %q and %q, want each read in its own mode", sent[0].Text, sent[1].Text)
	}
	if got := kinds(sent[1].Entities); !slices.Equal(got, []string{"italic:pudding"}) {
		t.Errorf("entities = %v, want the italic from the second caption", got)
	}

	_, err = b.SendMediaGroup(context.Background(), &bot.SendMediaGroupParams{
		ChatID: ada.ID(),
		Media: []models.InputMedia{
			&models.InputMediaPhoto{Media: "three", Caption: "fine"},
			&models.InputMediaPhoto{Media: "four", Caption: "*broken", ParseMode: models.ParseModeMarkdown},
		},
	})
	if err == nil {
		t.Error("the album was accepted, want the unreadable caption to refuse it")
	}
	if after := len(ada.History()); after != 2 {
		t.Errorf("history = %d messages, want nothing added by the refused album", after)
	}
}

func TestCodeMayHoldTheBacktickThatWouldEndIt(t *testing.T) {
	text, entities, err := styleOf("run "+"`git log \\`x\\``"+" first", "MarkdownV2")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if text != "run git log `x` first" {
		t.Errorf("text = %q, want the escaped backticks inside the code", text)
	}
	if got := kinds(entitiesOf(text, entities)); !slices.Equal(got, []string{"code:git log `x`"}) {
		t.Errorf("entities = %v, want one code span holding both", got)
	}
}
