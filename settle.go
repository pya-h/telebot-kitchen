package kitchen

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultWaitTimeout = 2 * time.Second

	// How long the conversation must stay quiet before Settle calls it over.
	quietPeriod = 50 * time.Millisecond
)

// activity wakes waiters whenever anything happens in the conversation, inbound
// delivery included: a test waits on the exchange, not on one side of it.
type activity struct {
	mu   sync.Mutex
	wake chan struct{}
}

func newActivity() *activity { return &activity{wake: make(chan struct{})} }

func (a *activity) note() {
	a.mu.Lock()
	defer a.mu.Unlock()

	close(a.wake)
	a.wake = make(chan struct{})
}

// watch hands back the channel the next note closes. Take it before testing a
// condition, or a note landing in between is missed.
func (a *activity) watch() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wake
}

// WaitFor blocks until cond holds, rechecking every time the bot calls the API
// or a user speaks. what names the condition in the failure message.
func (k *Kitchen) WaitFor(what string, cond func() bool) bool {
	if k.waitFor(cond) {
		return true
	}
	k.tb.Errorf("kitchen: waited %s for %s, which never happened", k.waitTimeout, what)
	return false
}

// waitFor leaves the reporting to the caller, which usually knows more about
// what was wanted than a description can carry.
func (k *Kitchen) waitFor(cond func() bool) bool {
	timeout := time.NewTimer(k.waitTimeout)
	defer timeout.Stop()

	for {
		wake := k.activity.watch()
		if cond() {
			return true
		}
		select {
		case <-wake:
		case <-timeout.C:
			return false
		}
	}
}

// Settle waits for the conversation to go quiet, for bots that reply from their
// own goroutine. Prefer WaitFor: this one can only watch the clock.
func (k *Kitchen) Settle() {
	timeout := time.NewTimer(k.waitTimeout)
	defer timeout.Stop()

	for {
		wake := k.activity.watch()
		rest := time.NewTimer(quietPeriod)
		select {
		case <-wake:
			rest.Stop()
		case <-rest.C:
			return
		case <-timeout.C:
			rest.Stop()
			k.tb.Errorf("kitchen: the bot was still working after %s, so nothing settled", k.waitTimeout)
			return
		}
	}
}

// ExpectReply waits for the bot's next message to this member and returns it;
// successive calls walk the replies in order.
func (m *Member) ExpectReply() Message {
	reply, _ := m.awaitReply()
	return reply
}

func (m *Member) awaitReply() (Message, bool) {
	var reply Message
	ok := m.kitchen().WaitFor(fmt.Sprintf("a reply to %s", m), func() bool {
		next, found := m.nextReply()
		reply = next
		return found
	})
	return reply, ok
}

// Only the test goroutine ever drives a user, so the watermark needs no lock.
func (m *Member) nextReply() (Message, bool) {
	next, ok := m.peekReply()
	if ok {
		m.awaiting = next.ID
	}
	return next, ok
}

func (m *Member) peekReply() (Message, bool) {
	for _, sent := range m.chat.History() {
		if sent.FromBot && sent.ID > m.awaiting {
			return sent, true
		}
	}
	return Message{}, false
}

// Whatever is on screen when a member acts is answered by what comes after it.
func (m *Member) awaitFromNow() {
	if sent, ok := m.kitchen().world.latest(m.chat.id); ok {
		m.awaiting = sent.ID
	}
}
