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
	mu    sync.Mutex
	wake  chan struct{}
	stirs map[int64]uint64
}

func newActivity() *activity {
	return &activity{wake: make(chan struct{}), stirs: map[int64]uint64{}}
}

// note wakes every waiter, and counts against the chats it happened in.
func (a *activity) note(chats ...int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, chat := range chats {
		a.stirs[chat]++
	}
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

// watchChat is watch, with how often this chat has stirred so far, read together
// so a note cannot land between the two.
func (a *activity) watchChat(chat int64) (<-chan struct{}, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wake, a.stirs[chat]
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

// Settle waits for this member's chat to go quiet, however busy the others are.
// Updates already on their way when it starts hold it too, whoever sent them,
// because a reply to one may land here; later ones hold only their own chat.
func (m *Member) Settle() {
	k := m.kitchen()
	timeout := time.NewTimer(k.waitTimeout)
	defer timeout.Stop()
	rest := time.NewTimer(quietPeriod)
	defer rest.Stop()

	mark := k.waiting.mark()
	_, seen := k.activity.watchChat(m.chat.id)
	held := false
	for {
		wake, stirs := k.activity.watchChat(m.chat.id)
		pending := !k.waiting.reached(mark)
		if pending || held || stirs != seen {
			seen = stirs
			rest.Reset(quietPeriod)
		}
		held = pending

		select {
		case <-wake:
		case <-rest.C:
			if !pending {
				return
			}
		case <-timeout.C:
			k.tb.Errorf("kitchen: the bot was still working on %s after %s, so nothing settled", m, k.waitTimeout)
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

// Only the goroutine driving this member touches the watermark, so it needs no lock.
func (m *Member) nextReply() (Message, bool) {
	next, ok := m.peekReply()
	if ok {
		m.awaiting = next.ID
	}
	return next, ok
}

// A waiter rechecks this on every stir, so it asks for the one message rather
// than rendering the whole chat.
func (m *Member) peekReply() (Message, bool) {
	k := m.kitchen()
	next, found := k.world.nextBy(m.chat.id, k.botUser().ID, m.awaiting)
	if !found {
		return Message{}, false
	}
	return k.view(next), true
}

// Whatever is on screen when a member acts is answered by what comes after it.
func (m *Member) awaitFromNow() {
	if sent, ok := m.kitchen().world.latest(m.chat.id); ok {
		m.readPast(sent.ID)
	}
}

// readPast is what acting does to the member's place in the chat, unless they
// are one who reads every reply.
func (m *Member) readPast(id int) {
	if !m.kitchen().readsEveryReply(m.user.id) {
		m.awaiting = id
	}
}
