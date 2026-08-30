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

// activity wakes waiters whenever anything happens in the conversation, so a
// test can block on what the bot did instead of on a sleep. Inbound delivery
// counts too: a test waits on the exchange, not on one side of it.
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
// condition — a note landing in between would otherwise be missed and the
// waiter would sleep through its own wake-up.
func (a *activity) watch() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wake
}

// WaitFor blocks until cond holds, rechecking every time the bot calls the API
// or a user speaks. what names the condition in the failure message.
func (k *Kitchen) WaitFor(what string, cond func() bool) bool {
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
			k.tb.Errorf("kitchen: waited %s for %s, which never happened", k.waitTimeout, what)
			return false
		}
	}
}

// Settle waits for the conversation to go quiet, for bots that reply from their
// own goroutine. It is the blunt instrument: WaitFor and ExpectReply know what
// they are waiting for, while this one can only watch the clock, so reach for it
// only when there is nothing specific to wait on.
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

// ExpectReply waits for the bot's next message to this user and returns it.
// Successive calls walk the replies in order, so a bot that answers twice is
// read one message at a time.
func (u *User) ExpectReply() Message {
	var reply Message
	u.kitchen.WaitFor(fmt.Sprintf("a reply to user %d", u.ID()), func() bool {
		m, ok := u.nextReply()
		reply = m
		return ok
	})
	return reply
}

// Only the test goroutine ever drives a user, so the watermark needs no lock.
func (u *User) nextReply() (Message, bool) {
	for _, m := range u.kitchen.History(u.chatID) {
		if m.FromBot && m.ID > u.awaiting {
			u.awaiting = m.ID
			return m, true
		}
	}
	return Message{}, false
}

// Whatever is on screen when a user acts is answered by what comes after it.
func (u *User) awaitFromNow() {
	if m, ok := u.kitchen.world.latest(u.chatID); ok {
		u.awaiting = m.ID
	}
}
