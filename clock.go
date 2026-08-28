package kitchen

import (
	"sync"
	"time"
)

// Telegram reads a zero date as an inaccessible message, so the kitchen's time
// starts at a fixed non-zero instant instead of the zero value.
var defaultStartTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// Clock is the kitchen's only source of time; it moves when a test advances it.
type Clock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *Clock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
