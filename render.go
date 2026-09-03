package kitchen

import "strings"

// String renders the message the way a client shows it: the text, then the
// keyboard a row to a line.
func (m Message) String() string {
	var parts []string
	if m.ForwardedFrom != "" {
		parts = append(parts, "(forwarded from "+m.ForwardedFrom+")")
	}
	if m.Media != "" {
		parts = append(parts, "("+m.Media+")")
	}
	if m.Event != "" {
		parts = append(parts, "("+m.Event+")")
	}
	if m.Text != "" {
		parts = append(parts, m.Text)
	}

	var out strings.Builder
	out.WriteString(strings.Join(parts, " "))

	for _, row := range m.Keyboard {
		out.WriteString("\n")
		for i, button := range row {
			if i > 0 {
				out.WriteString(" ")
			}
			out.WriteString("[" + button.Label + "]")
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
		entries[i] = "**" + m.From + ":** " + m.String()
	}
	return strings.Join(entries, "\n\n") + "\n"
}

func (m *Member) Transcript() string { return m.chat.Transcript() }
