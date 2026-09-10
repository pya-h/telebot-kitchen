package kitchen

import (
	"cmp"
	"strings"
	"unicode"
)

var rightToLeft = []*unicode.RangeTable{
	unicode.Arabic, unicode.Hebrew, unicode.Syriac, unicode.Thaana, unicode.Nko,
}

// isolated fences text that runs right to left, and leaves anything that does
// not exactly as it was: a transcript with no such text reads as plain bytes.
func isolated(text string) string {
	if !strings.ContainsFunc(text, func(r rune) bool { return unicode.In(r, rightToLeft...) }) {
		return text
	}
	return "\u2068" + text + "\u2069"
}

// reads is the text with whatever the bot marked up written back on, and the
// plain text for a message nobody built through this package.
func (m Message) reads() string {
	if m.rich == "" {
		return m.Text
	}
	return m.rich
}

// String renders the message the way a client shows it: the text, then the
// keyboard a row to a line.
func (m Message) String() string {
	var parts []string
	if m.ForwardedFrom != "" {
		parts = append(parts, "(forwarded from "+m.ForwardedFrom+")")
	}
	if m.Media != "" {
		parts = append(parts, "("+isolated(cmp.Or(m.carries, m.Media))+")")
	}
	if m.Event != "" {
		parts = append(parts, "("+m.Event+")")
	}
	if m.Text != "" {
		parts = append(parts, isolated(m.reads()))
	}

	var out strings.Builder
	out.WriteString(strings.Join(parts, " "))

	for _, option := range m.Options {
		out.WriteString("\n- ")
		out.WriteString(isolated(option))
	}

	if len(m.Reactions) > 0 {
		out.WriteString("\n")
		out.WriteString(strings.Join(m.Reactions, " "))
	}

	for _, row := range m.Keyboard {
		out.WriteString("\n")
		for i, button := range row {
			if i > 0 {
				out.WriteString(" ")
			}
			out.WriteString("[")
			out.WriteString(isolated(button.Label))
			out.WriteString("]")
		}
	}

	if out.Len() == 0 {
		return "(nothing)"
	}
	return out.String()
}

// Transcript renders the chat as a readable back-and-forth, for a failure
// message or a golden file.
func (k *Kitchen) Transcript(chatID int64) string {
	log := k.History(chatID)
	if len(log) == 0 {
		return ""
	}

	entries := make([]string, len(log))
	for i, m := range log {
		entries[i] = m.String()
		if m.From != "" {
			entries[i] = "**" + isolated(m.From) + ":** " + entries[i]
		}
	}
	return strings.Join(entries, "\n\n") + "\n"
}

func (m *Member) Transcript() string { return m.chat.Transcript() }
