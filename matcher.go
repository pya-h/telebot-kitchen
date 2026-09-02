package kitchen

import (
	"fmt"
	"strings"
)

// Matcher picks calls out of the record and can say what it was looking for, so
// a failed assertion reads as a sentence instead of a bare boolean.
type Matcher struct {
	what  string
	match func(Call) bool
}

func (m Matcher) String() string { return m.what }

func Method(name string) Matcher {
	return Matcher{"method " + name, func(c Call) bool { return c.Method == name }}
}

func ToChat(id int64) Matcher {
	return Matcher{fmt.Sprintf("to chat %d", id), func(c Call) bool { return c.ChatID == id }}
}

func ToUser(u *User) Matcher { return ToChat(u.ChatID()) }

func TextIs(text string) Matcher {
	return Matcher{fmt.Sprintf("text %q", text), func(c Call) bool { return c.Text() == text }}
}

func TextContains(part string) Matcher {
	return Matcher{fmt.Sprintf("text containing %q", part), func(c Call) bool {
		return strings.Contains(c.Text(), part)
	}}
}

func HasButton(labelOrData string) Matcher {
	return Matcher{fmt.Sprintf("button %q", labelOrData), func(c Call) bool {
		_, ok := findButton(c.Keyboard(), labelOrData)
		return ok
	}}
}

// Param reaches parameters the named matchers do not cover, so an assertion is
// never blocked on the toolbox growing a matcher for it first.
func Param(name, value string) Matcher {
	return Matcher{fmt.Sprintf("%s=%q", name, value), func(c Call) bool {
		return c.Params[name] == value
	}}
}

func All(ms ...Matcher) Matcher {
	if len(ms) == 0 {
		return Matcher{"any call", func(Call) bool { return true }}
	}
	return Matcher{describe(ms, " and "), func(c Call) bool {
		for _, m := range ms {
			if !m.match(c) {
				return false
			}
		}
		return true
	}}
}

func Any(ms ...Matcher) Matcher {
	return Matcher{describe(ms, " or "), func(c Call) bool {
		for _, m := range ms {
			if m.match(c) {
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
