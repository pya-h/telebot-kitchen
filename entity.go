package kitchen

import (
	"html"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"
)

type Entity struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	URL  string `json:"url,omitempty"` // where a link points
}

func cantParse(why string) *apiError { return requestError("can't parse entities: " + why) }

type styled struct {
	text  strings.Builder
	at    int
	stack []span
	done  []models.MessageEntity
}

type span struct {
	kind models.MessageEntityType
	url  string
	lang string
	from int
}

const insidePre = models.MessageEntityType("")

func (s *styled) write(text string) {
	s.text.WriteString(text)
	s.at += utf16Len(text)
}

func (s *styled) push(kind models.MessageEntityType, url, lang string) {
	s.stack = append(s.stack, span{kind: kind, url: url, lang: lang, from: s.at})
}

func (s *styled) pop(kind models.MessageEntityType) bool {
	last := len(s.stack) - 1
	if last < 0 || s.stack[last].kind != kind {
		return false
	}
	one := s.stack[last]
	s.stack = s.stack[:last]

	if one.kind != insidePre && s.at > one.from {
		s.done = append(s.done, models.MessageEntity{
			Type: one.kind, Offset: one.from, Length: s.at - one.from,
			URL: one.url, Language: one.lang,
		})
	}
	return true
}

func (s *styled) toggle(kind models.MessageEntityType) error {
	if slices.ContainsFunc(s.stack, func(one span) bool { return one.kind == kind }) {
		if !s.pop(kind) {
			return cantParse("can't find end of " + string(s.stack[len(s.stack)-1].kind) + " entity")
		}
		return nil
	}
	s.push(kind, "", "")
	return nil
}

func (s *styled) carry(text string, entities []models.MessageEntity) {
	base := s.at
	s.write(text)
	for _, e := range entities {
		e.Offset += base
		s.done = append(s.done, e)
	}
}

func (s *styled) finish() (string, []models.MessageEntity, error) {
	if len(s.stack) > 0 {
		return "", nil, cantParse("can't find end of " + string(s.stack[len(s.stack)-1].kind) + " entity")
	}
	slices.SortStableFunc(s.done, func(a, b models.MessageEntity) int {
		if a.Offset != b.Offset {
			return a.Offset - b.Offset
		}
		return b.Length - a.Length
	})
	return s.text.String(), s.done, nil
}

func styleOf(text, mode string) (string, []models.MessageEntity, error) {
	switch mode {
	case "":
		return text, nil, nil
	case "HTML":
		return parseHTML(text)
	case "MarkdownV2":
		return parseMarkdown(text, true)
	case "Markdown":
		return parseMarkdown(text, false)
	}
	return "", nil, cantParse("unsupported parse mode " + strconv.Quote(mode))
}

var htmlTags = map[string]models.MessageEntityType{
	"b":          models.MessageEntityTypeBold,
	"strong":     models.MessageEntityTypeBold,
	"i":          models.MessageEntityTypeItalic,
	"em":         models.MessageEntityTypeItalic,
	"u":          models.MessageEntityTypeUnderline,
	"ins":        models.MessageEntityTypeUnderline,
	"s":          models.MessageEntityTypeStrikethrough,
	"strike":     models.MessageEntityTypeStrikethrough,
	"del":        models.MessageEntityTypeStrikethrough,
	"tg-spoiler": models.MessageEntityTypeSpoiler,
	"span":       models.MessageEntityTypeSpoiler,
	"blockquote": models.MessageEntityTypeBlockquote,
	"code":       models.MessageEntityTypeCode,
	"pre":        models.MessageEntityTypePre,
	"a":          models.MessageEntityTypeTextLink,
}

func parseHTML(text string) (string, []models.MessageEntity, error) {
	s := &styled{}
	for {
		open := strings.IndexByte(text, '<')
		if open < 0 {
			s.write(html.UnescapeString(text))
			return s.finish()
		}
		s.write(html.UnescapeString(text[:open]))

		shut := strings.IndexByte(text[open:], '>')
		if shut < 0 {
			return "", nil, cantParse("unclosed start tag")
		}
		tag := text[open+1 : open+shut]
		text = text[open+shut+1:]
		if err := s.tag(tag); err != nil {
			return "", nil, err
		}
	}
}

func (s *styled) tag(tag string) error {
	if name, closing := strings.CutPrefix(tag, "/"); closing {
		kind, known := htmlTags[strings.ToLower(strings.TrimSpace(name))]
		if !known {
			return cantParse("unsupported end tag " + strconv.Quote(name))
		}
		if kind == models.MessageEntityTypeCode && s.topIs(insidePre) {
			kind = insidePre
		}
		if !s.pop(kind) {
			return cantParse("unexpected end tag " + strconv.Quote(name))
		}
		return nil
	}

	name, attrs, _ := strings.Cut(tag, " ")
	kind, known := htmlTags[strings.ToLower(name)]
	if !known {
		return cantParse("unsupported start tag " + strconv.Quote(name))
	}
	switch {
	case kind == models.MessageEntityTypeSpoiler && name == "span" && !strings.Contains(attrs, "tg-spoiler"):
		return cantParse(`unsupported start tag "span"`)
	case kind == models.MessageEntityTypeTextLink:
		s.push(kind, attributeIn(attrs, "href"), "")
	case kind == models.MessageEntityTypeCode && s.topIs(models.MessageEntityTypePre):
		s.stack[len(s.stack)-1].lang = strings.TrimPrefix(attributeIn(attrs, "class"), "language-")
		s.push(insidePre, "", "")
	default:
		s.push(kind, "", "")
	}
	return nil
}

func (s *styled) topIs(kind models.MessageEntityType) bool {
	last := len(s.stack) - 1
	return last >= 0 && s.stack[last].kind == kind
}

func attributeIn(attrs, name string) string {
	_, rest, found := strings.Cut(attrs, name+"=")
	if !found {
		return ""
	}
	rest = strings.TrimLeft(rest, `"'`)
	if end := strings.IndexAny(rest, `"'`); end >= 0 {
		rest = rest[:end]
	}
	// A URL is written with &amp; to be HTML at all, and read back as &.
	return html.UnescapeString(rest)
}

// The delimiters, longest first so "__" is not read as two italics.
var markdownMarks = []struct {
	mark string
	kind models.MessageEntityType
	v2   bool
}{
	{"||", models.MessageEntityTypeSpoiler, true},
	{"__", models.MessageEntityTypeUnderline, true},
	{"~", models.MessageEntityTypeStrikethrough, true},
	{"*", models.MessageEntityTypeBold, false},
	{"_", models.MessageEntityTypeItalic, false},
}

func parseMarkdown(text string, v2 bool) (string, []models.MessageEntity, error) {
	s := &styled{}
	for i := 0; i < len(text); {
		rest := text[i:]
		switch {
		// An escape is the character itself, whatever it would have meant.
		case v2 && rest[0] == '\\' && len(rest) > 1:
			r, size := utf8.DecodeRuneInString(rest[1:])
			s.write(string(r))
			i += 1 + size

		case strings.HasPrefix(rest, "```"):
			width, err := s.fence(rest, v2)
			if err != nil {
				return "", nil, err
			}
			i += width

		case rest[0] == '`':
			width, err := s.span(rest, v2)
			if err != nil {
				return "", nil, err
			}
			i += width

		case rest[0] == '[':
			width, err := s.link(rest, v2)
			if err != nil {
				return "", nil, err
			}
			i += width

		default:
			if mark, kind, ok := markdownAt(rest, v2); ok {
				if err := s.toggle(kind); err != nil {
					return "", nil, err
				}
				i += len(mark)
				continue
			}
			r, size := utf8.DecodeRuneInString(rest)
			s.write(string(r))
			i += size
		}
	}
	return s.finish()
}

func markdownAt(rest string, v2 bool) (string, models.MessageEntityType, bool) {
	for _, one := range markdownMarks {
		if (v2 || !one.v2) && strings.HasPrefix(rest, one.mark) {
			return one.mark, one.kind, true
		}
	}
	return "", "", false
}

// fence reads a code block, whose first line may name the language it is in.
func (s *styled) fence(rest string, v2 bool) (int, error) {
	end := unescaped(rest[3:], "```", v2)
	if end < 0 {
		return 0, cantParse("can't find end of pre entity")
	}
	body := rest[3 : 3+end]

	lang := ""
	if head, tail, split := strings.Cut(body, "\n"); split && !strings.ContainsAny(head, " \t") {
		lang, body = head, tail
	}
	s.push(models.MessageEntityTypePre, "", lang)
	s.write(verbatim(body, v2))
	s.pop(models.MessageEntityTypePre)
	return 3 + end + 3, nil
}

func (s *styled) span(rest string, v2 bool) (int, error) {
	end := unescaped(rest[1:], "`", v2)
	if end < 0 {
		return 0, cantParse("can't find end of code entity")
	}
	s.push(models.MessageEntityTypeCode, "", "")
	s.write(verbatim(rest[1:1+end], v2))
	s.pop(models.MessageEntityTypeCode)
	return 1 + end + 1, nil
}

// A backtick is how code ends, so one inside it has to be escaped past.
func unescaped(rest, mark string, v2 bool) int {
	for i := 0; i < len(rest); i++ {
		if v2 && rest[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(rest[i:], mark) {
			return i
		}
	}
	return -1
}

// Nothing is marked up inside code, but an escape is still an escape.
func verbatim(body string, v2 bool) string {
	if !v2 || !strings.Contains(body, `\`) {
		return body
	}
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) {
			i++
		}
		out.WriteByte(body[i])
	}
	return out.String()
}

// link parses the label with the same rules, so a link may carry markup of its
// own without a second pass over text the offsets already describe.
func (s *styled) link(rest string, v2 bool) (int, error) {
	label := closingIndex(rest, '[', ']', v2)
	if label < 0 || label+1 >= len(rest) || rest[label+1] != '(' {
		s.write("[")
		return 1, nil
	}
	target := closingIndex(rest[label+1:], '(', ')', v2)
	if target < 0 {
		s.write("[")
		return 1, nil
	}

	inner, entities, err := parseMarkdown(rest[1:label], v2)
	if err != nil {
		return 0, err
	}
	s.push(models.MessageEntityTypeTextLink, verbatim(rest[label+2:label+1+target], v2), "")
	s.carry(inner, entities)
	s.pop(models.MessageEntityTypeTextLink)
	return label + 1 + target + 1, nil
}

func closingIndex(rest string, open, shut byte, v2 bool) int {
	depth := 0
	for i := 0; i < len(rest); i++ {
		switch {
		case v2 && rest[i] == '\\':
			i++
		case rest[i] == open:
			depth++
		case rest[i] == shut:
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

var markdownAround = map[models.MessageEntityType][2]string{
	models.MessageEntityTypeBold:          {"**", "**"},
	models.MessageEntityTypeItalic:        {"_", "_"},
	models.MessageEntityTypeUnderline:     {"__", "__"},
	models.MessageEntityTypeStrikethrough: {"~~", "~~"},
	models.MessageEntityTypeSpoiler:       {"||", "||"},
	models.MessageEntityTypeCode:          {"`", "`"},
}

// markdownOf writes the markup back on, so a transcript shows what a client
// shows rather than the offsets it was described with.
func markdownOf(text string, entities []models.MessageEntity) string {
	units := utf16.Encode([]rune(text))
	marked := make([]models.MessageEntity, 0, len(entities))
	for _, e := range entities {
		// A bot may describe a span that is not there; a client shows the text.
		if e.Offset >= 0 && e.Length > 0 && e.Offset+e.Length <= len(units) {
			marked = append(marked, e)
		}
	}
	if len(marked) == 0 {
		return text
	}
	// Outermost first where two start together, so the wrappers nest.
	slices.SortStableFunc(marked, func(a, b models.MessageEntity) int {
		if a.Offset != b.Offset {
			return a.Offset - b.Offset
		}
		return b.Length - a.Length
	})

	opens, shuts := map[int][]models.MessageEntity{}, map[int][]models.MessageEntity{}
	for _, e := range marked {
		opens[e.Offset] = append(opens[e.Offset], e)
		shuts[e.Offset+e.Length] = append(shuts[e.Offset+e.Length], e)
	}

	var out strings.Builder
	quoted := 0
	for at := 0; at <= len(units); at++ {
		// Innermost closes first, which is the reverse of the order they opened.
		for _, e := range slices.Backward(shuts[at]) {
			if e.Type == models.MessageEntityTypeBlockquote {
				quoted--
			}
			out.WriteString(closerOf(e, out.String()))
		}
		for _, e := range opens[at] {
			if e.Type == models.MessageEntityTypeBlockquote {
				quoted++
			}
			out.WriteString(openerOf(e))
		}
		if at == len(units) {
			break
		}

		width := 1
		if utf16.IsSurrogate(rune(units[at])) && at+1 < len(units) {
			width = 2
		}
		if letter := string(utf16.Decode(units[at : at+width])); letter == "\n" && quoted > 0 {
			out.WriteString("\n> ")
		} else {
			out.WriteString(letter)
		}
		at += width - 1
	}
	return out.String()
}

func openerOf(e models.MessageEntity) string {
	switch e.Type {
	case models.MessageEntityTypeTextLink:
		return "["
	case models.MessageEntityTypePre:
		return "```" + e.Language + "\n"
	case models.MessageEntityTypeBlockquote, models.MessageEntityTypeExpandableBlockquote:
		return "> "
	}
	return markdownAround[e.Type][0]
}

func closerOf(e models.MessageEntity, sofar string) string {
	switch e.Type {
	case models.MessageEntityTypeTextLink:
		return "](" + e.URL + ")"
	case models.MessageEntityTypePre:
		if strings.HasSuffix(sofar, "\n") {
			return "```"
		}
		return "\n```"
	case models.MessageEntityTypeBlockquote, models.MessageEntityTypeExpandableBlockquote:
		return ""
	}
	return markdownAround[e.Type][1]
}

func entitiesOf(text string, entities []models.MessageEntity) []Entity {
	if len(entities) == 0 {
		return nil
	}
	units := utf16.Encode([]rune(text))
	marked := make([]Entity, 0, len(entities))
	for _, e := range entities {
		if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > len(units) {
			continue
		}
		marked = append(marked, Entity{
			Kind: string(e.Type),
			Text: string(utf16.Decode(units[e.Offset : e.Offset+e.Length])),
			URL:  e.URL,
		})
	}
	return marked
}

// styled reads a text or caption parameter along with whatever the bot said
// about its markup. Explicit entities win over a parse mode, as they do live.
func (p params) styled(field, marked string) (string, []models.MessageEntity, error) {
	var given []models.MessageEntity
	if err := p.decode(marked, &given); err != nil {
		return "", nil, badRequest(marked)
	}
	return styledText(p[field], p["parse_mode"], given)
}

func styledText(text, mode string, given []models.MessageEntity) (string, []models.MessageEntity, error) {
	if len(given) > 0 {
		return text, given, nil
	}
	return styleOf(text, mode)
}

// Counted once the markup is off, in the UTF-16 units entities are measured in.
const (
	mostText    = 4096
	mostCaption = 1024
)

func (p params) text() (string, []models.MessageEntity, error) {
	text, entities, err := p.styled("text", "entities")
	switch {
	case err != nil:
		return "", nil, err
	case text == "":
		return "", nil, requestError("message text is empty")
	case utf16Len(text) > mostText:
		return "", nil, requestError("message is too long")
	}
	return text, entities, nil
}

func (p params) caption() (string, []models.MessageEntity, error) {
	return fitCaption(p.styled("caption", "caption_entities"))
}

func fitCaption(caption string, entities []models.MessageEntity, err error) (string, []models.MessageEntity, error) {
	if err == nil && utf16Len(caption) > mostCaption {
		return "", nil, requestError("message caption is too long")
	}
	return caption, entities, err
}
