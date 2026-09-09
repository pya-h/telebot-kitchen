package kitchen

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
)

// pollMethod is the one call that is the bot asking whether anything happened,
// rather than something happening. It neither wakes waiters nor lands in the
// call log: noting it would keep a conversation from ever going quiet, and
// recording it would bury everything else a failure message has to show.
const pollMethod = "getUpdates"

// Telegram hands out at most this many updates in one answer.
const mostUpdates = 100

// pollQueue holds what a polling bot has not taken yet. Updates stay until an
// offset confirms them, so a bot that never advances one is handed the same
// updates again, exactly as Telegram does.
type pollQueue struct {
	mu      sync.Mutex
	waiting []models.Update
	polling bool
}

func newPollQueue() *pollQueue { return &pollQueue{} }

func (q *pollQueue) add(u models.Update) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.waiting = append(q.waiting, u)
}

// begin claims the queue for one poll. Telegram lets a second poller find out
// the hard way that it is the second.
func (q *pollQueue) begin() bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.polling {
		return false
	}
	q.polling = true
	return true
}

func (q *pollQueue) end() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.polling = false
}

// busy says whether a poll is holding the queue, which a test watches for
// rather than guessing at with a sleep.
func (q *pollQueue) busy() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.polling
}

// confirm drops what the offset says the bot already has.
func (q *pollQueue) confirm(offset int64) {
	q.mu.Lock()
	defer q.mu.Unlock()

	kept := q.waiting[:0]
	for _, u := range q.waiting {
		if u.ID >= offset {
			kept = append(kept, u)
		}
	}
	q.waiting = kept
}

// held is how much the queue is still carrying, which only a test asks about.
func (q *pollQueue) held() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiting)
}

func (q *pollQueue) peek(offset int64, limit int) []models.Update {
	q.mu.Lock()
	defer q.mu.Unlock()

	got := make([]models.Update, 0, limit)
	for _, u := range q.waiting {
		if u.ID < offset {
			continue
		}
		if got = append(got, u); len(got) == limit {
			break
		}
	}
	return got
}

// DeliverByPolling queues updates for a bot that fetches them itself, which is
// the third way in and the only one where the kitchen pushes nothing.
func (k *Kitchen) DeliverByPolling() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.polling, k.process, k.hook = true, nil, nil
}

func (k *Kitchen) getUpdates(p params) (any, error) {
	k.mu.RLock()
	hooked, polling := k.webhook.url != "", k.polling
	k.mu.RUnlock()

	// The two cannot both be the way in, and Telegram says so rather than
	// quietly handing out nothing.
	if hooked {
		return nil, conflict("can't use getUpdates method while webhook is active")
	}
	if !polling {
		k.reportPollingUnbound()
	}

	if !k.updates.begin() {
		return nil, conflict("terminated by other getUpdates request; make sure that only one bot instance is running")
	}
	defer k.updates.end()

	offset := int64(p.number("offset"))
	k.updates.confirm(offset)

	limit := p.number("limit")
	if limit <= 0 || limit > mostUpdates {
		limit = mostUpdates
	}

	// A poll waits for as long as it asked, but never past the kitchen's own
	// bound: a test that has nothing coming should fail on its assertion rather
	// than on a bot's thirty-second timeout.
	waiting := k.waitTimeout
	if asked := time.Duration(p.number("timeout")) * time.Second; asked > 0 && asked < waiting {
		waiting = asked
	}
	timeout := time.NewTimer(waiting)
	defer timeout.Stop()

	for {
		wake := k.activity.watch()
		if got := k.updates.peek(offset, limit); len(got) > 0 {
			return got, nil
		}
		select {
		case <-wake:
		case <-timeout.C:
			return []models.Update{}, nil
		case <-k.closing:
			return []models.Update{}, nil
		}
	}
}

// A bot polling a kitchen that is pushing gets nothing, forever and silently,
// which is the worst way for a test to fail.
func (k *Kitchen) reportPollingUnbound() {
	k.polledUnbound.Do(func() {
		k.tb.Errorf("kitchen: the bot is polling for updates, but the kitchen was not told to queue them; call DeliverByPolling")
	})
}

func conflict(description string) *apiError {
	return &apiError{Code: http.StatusConflict, Description: "Conflict: " + description}
}
