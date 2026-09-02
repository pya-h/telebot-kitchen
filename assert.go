package kitchen

import (
	"strconv"
	"strings"
)

// Expect asserts the bot made a call like this and returns the last one that did.
func (k *Kitchen) Expect(ms ...Matcher) Call {
	calls := k.Calls()
	call, ok := calls.Last(ms...)
	if !ok {
		k.tb.Errorf("kitchen: no call with %s, the bot made:\n%s", All(ms...), calls)
	}
	return call
}

func (k *Kitchen) ExpectNo(ms ...Matcher) {
	if found := k.Calls().Matching(ms...); len(found) > 0 {
		k.tb.Errorf("kitchen: expected no call with %s, but the bot made:\n%s", All(ms...), found)
	}
}

func (k *Kitchen) ExpectCount(n int, ms ...Matcher) {
	found := k.Calls().Matching(ms...)
	if len(found) != n {
		k.tb.Errorf("kitchen: %d calls with %s, want %d:\n%s", len(found), All(ms...), n, found)
	}
}

// Expect waits for the bot's next reply and asserts it looks like this;
// successive calls walk the replies in order.
func (u *User) Expect(ms ...Matcher) Message {
	reply, ok := u.awaitReply()
	if !ok {
		return reply
	}
	if want := All(ms...); !want.match(reply.subject()) {
		u.kitchen.tb.Errorf("kitchen: user %d was told %q with buttons %s, want %s",
			u.ID(), reply.Text, buttonLabels(reply.Keyboard), want)
	}
	return reply
}

// ExpectScreen waits until the user's screen looks like this, then returns it.
// A bot that edits its menu in place sends nothing new, so waiting on the screen
// is the only way to follow it.
func (u *User) ExpectScreen(ms ...Matcher) Screen {
	want := All(ms...)

	var screen Screen
	if !u.kitchen.waitFor(func() bool {
		screen = u.Screen()
		return want.match(screen.subject())
	}) {
		u.kitchen.tb.Errorf("kitchen: user %d sees %q with buttons %s, want %s",
			u.ID(), screen.Text, buttonLabels(screen.Keyboard), want)
	}
	return screen
}

// ExpectNothingMore asserts the bot sent nothing past the replies already read.
// It settles first, so it costs the quiet period.
func (u *User) ExpectNothingMore() {
	u.kitchen.Settle()
	if extra, ok := u.peekReply(); ok {
		u.kitchen.tb.Errorf("kitchen: user %d was also told %q, which no assertion expected", u.ID(), extra.Text)
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
			k.tb.Errorf("kitchen: skipped %s, the scenario had already failed", names(steps[i:]))
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
