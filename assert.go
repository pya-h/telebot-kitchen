package kitchen

import (
	"strconv"
	"strings"
)

// Expect waits for the bot to make a call like this and returns the last one
// that did.
func (k *Kitchen) Expect(ms ...Matcher) Call {
	want := All(ms...)

	var call Call
	if !k.waitFor(func() bool {
		last, ok := k.Calls().Last(want)
		call = last
		return ok
	}) {
		k.tb.Errorf("kitchen: no call with %s, the bot made:\n%s", want, k.Calls())
	}
	return call
}

// ExpectNo settles first: a call the bot has not made yet is not a call it never
// makes. The same goes for ExpectCount.
func (k *Kitchen) ExpectNo(ms ...Matcher) {
	k.Settle()
	if found := k.Calls().Matching(ms...); len(found) > 0 {
		k.tb.Errorf("kitchen: expected no call with %s, but the bot made:\n%s", All(ms...), found)
	}
}

func (k *Kitchen) ExpectCount(n int, ms ...Matcher) {
	k.Settle()
	found := k.Calls().Matching(ms...)
	if len(found) != n {
		k.tb.Errorf("kitchen: %d calls with %s, want %d:\n%s", len(found), All(ms...), n, found)
	}
}

// Expect waits for the bot's next reply and asserts it looks like this;
// successive calls walk the replies in order.
func (m *Member) Expect(ms ...Matcher) Message {
	reply, ok := m.awaitReply()
	if !ok {
		return reply
	}
	if want := All(ms...); !want.match(reply.subject()) {
		m.kitchen().tb.Errorf("kitchen: %s was told:\n%s\nwant %s", m, reply, want)
	}
	return reply
}

// ExpectScreen waits until the chat's screen looks like this, then returns it.
// A bot that edits its menu in place sends nothing new, so waiting on the screen
// is the only way to follow it.
func (m *Member) ExpectScreen(ms ...Matcher) Screen {
	want := All(ms...)

	var screen Screen
	if !m.kitchen().waitFor(func() bool {
		screen = m.Screen()
		return want.match(screen.subject())
	}) {
		m.kitchen().tb.Errorf("kitchen: %s sees:\n%s\nwant %s", m, screen, want)
	}
	return screen
}

// ExpectNothingMore asserts the bot sent nothing past the replies already read.
// It settles first, so it costs the quiet period.
func (m *Member) ExpectNothingMore() {
	m.kitchen().Settle()
	if extra, ok := m.peekReply(); ok {
		m.kitchen().tb.Errorf("kitchen: %s was also told %q, which no assertion expected", m, extra)
	}
}

type Step struct {
	Name string
	Do   func()
}

// Scenario runs steps in order, skipping the rest once one fails: later steps
// assume state the broken one never produced.
func (k *Kitchen) Scenario(steps ...Step) {
	for i, step := range steps {
		if k.tb.Failed() {
			k.tb.Errorf("kitchen: skipped %s, the test had already failed", names(steps[i:]))
			return
		}
		step.Do()
	}
}

func names(steps []Step) string {
	quoted := make([]string, len(steps))
	for i, step := range steps {
		quoted[i] = strconv.Quote(step.Name)
	}
	return strings.Join(quoted, ", ")
}
