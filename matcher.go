package kitchen

import (
	"fmt"
	"strings"
)

// subject is whatever a matcher inspects: a call the bot made, or a message on screen.
type subject struct {
	method   string
	chatID   int64
	text     string
	keyboard [][]Button
	params   map[string]string
}

// Matcher picks out the call or message a test wants, and can say what that was
// so a failure reads as a sentence.
type Matcher struct {
	what  string
	match func(subject) bool
}

func (m Matcher) String() string { return m.what }

func Method(name string) Matcher {
	return Matcher{"method " + name, func(s subject) bool { return s.method == name }}
}

func ToChat(id int64) Matcher {
	return Matcher{fmt.Sprintf("to chat %d", id), func(s subject) bool { return s.chatID == id }}
}

func ToUser(u *User) Matcher { return ToChat(u.ChatID()) }

func TextIs(text string) Matcher {
	return Matcher{fmt.Sprintf("text %q", text), func(s subject) bool { return s.text == text }}
}

func TextContains(part string) Matcher {
	return Matcher{fmt.Sprintf("text containing %q", part), func(s subject) bool {
		return strings.Contains(s.text, part)
	}}
}

func HasButton(labelOrData string) Matcher {
	return Matcher{fmt.Sprintf("button %q", labelOrData), func(s subject) bool {
		_, ok := findButton(s.keyboard, labelOrData)
		return ok
	}}
}

// Param reaches parameters the named matchers do not cover.
func Param(name, value string) Matcher {
	return Matcher{fmt.Sprintf("%s=%q", name, value), func(s subject) bool {
		return s.params[name] == value
	}}
}

func All(ms ...Matcher) Matcher {
	if len(ms) == 0 {
		return Matcher{"anything", func(subject) bool { return true }}
	}
	return Matcher{describe(ms, " and "), func(s subject) bool {
		for _, m := range ms {
			if !m.match(s) {
				return false
			}
		}
		return true
	}}
}

func Any(ms ...Matcher) Matcher {
	return Matcher{describe(ms, " or "), func(s subject) bool {
		for _, m := range ms {
			if m.match(s) {
				return true
			}
		}
		return false
	}}
}

func describe(ms []Matcher, sep string) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = m.what
	}
	return strings.Join(parts, sep)
}
