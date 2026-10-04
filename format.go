package kitchen

import (
	"fmt"
	"slices"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"
)

// Formatted is text as a client sends it: either markup a client would turn
// into spans, or the text with its spans already given.
type Formatted struct {
	text  string
	mode  string
	spans []Span
}

// Span marks part of a text, measured in UTF-16 code units as Telegram measures
// it. URL is for a text_link, User for a text_mention, CustomEmoji for a
// custom_emoji, and Language for a pre.
type Span struct {
	Kind        string
	Offset      int
	Length      int
	URL         string
	User        *User
	CustomEmoji string
	Language    string
}

func HTML(markup string) Formatted { return Formatted{text: markup, mode: "HTML"} }

func MarkdownV2(markup string) Formatted { return Formatted{text: markup, mode: "MarkdownV2"} }

func Markdown(markup string) Formatted { return Formatted{text: markup, mode: "Markdown"} }

func Plain(text string, spans ...Span) Formatted {
	return Formatted{text: text, spans: slices.Clone(spans)}
}

var spanKinds = []string{
	"mention", "hashtag", "cashtag", "bot_command", "url", "email", "phone_number",
	"bold", "italic", "underline", "strikethrough", "spoiler", "blockquote",
	"expandable_blockquote", "code", "pre", "text_link", "text_mention", "custom_emoji",
}

// missing is what the span needs to point at and was not given.
func (s Span) missing() string {
	switch {
	case s.Kind == "text_link" && s.URL == "":
		return "a URL"
	case s.Kind == "text_mention" && s.User == nil:
		return "a user"
	case s.Kind == "custom_emoji" && s.CustomEmoji == "":
		return "an emoji id"
	}
	return ""
}

// resolve is the text and its entities the way the bot will read them. A span
// a client could never send is the test's mistake, not something to model.
func (f Formatted) resolve() (string, []models.MessageEntity, error) {
	if f.mode != "" {
		text, entities, err := styleOf(f.text, f.mode)
		if err != nil {
			return "", nil, fmt.Errorf("%q does not read as %s: %w", f.text, f.mode, err)
		}
		return text, entities, nil
	}

	units := utf16.Encode([]rune(f.text))
	var entities []models.MessageEntity
	for _, s := range f.spans {
		switch {
		case !slices.Contains(spanKinds, s.Kind):
			return "", nil, fmt.Errorf("Telegram has no %q span", s.Kind)
		case s.missing() != "":
			return "", nil, fmt.Errorf("a %s span needs %s", s.Kind, s.missing())
		case s.Offset < 0 || s.Length <= 0 || s.Offset+s.Length > len(units):
			return "", nil, fmt.Errorf("a %s span at %d+%d does not lie inside %q, which is %d UTF-16 units long",
				s.Kind, s.Offset, s.Length, f.text, len(units))
		case halves(units, s.Offset) || halves(units, s.Offset+s.Length):
			return "", nil, fmt.Errorf("a %s span at %d+%d splits a character of %q in two", s.Kind, s.Offset, s.Length, f.text)
		}
		e := models.MessageEntity{
			Type: models.MessageEntityType(s.Kind), Offset: s.Offset, Length: s.Length,
			URL: s.URL, CustomEmojiID: s.CustomEmoji, Language: s.Language,
		}
		if s.User != nil {
			who := s.User.identity()
			e.User = &who
		}
		entities = append(entities, e)
	}
	slices.SortStableFunc(entities, outermostFirst)
	return f.text, entities, nil
}
