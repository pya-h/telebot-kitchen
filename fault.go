package kitchen

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Fault is how the kitchen refuses a call: the reply Telegram sends when it is
// unwilling, or no usable reply at all.
type Fault struct {
	what  string
	serve func(http.ResponseWriter)
}

func (f Fault) Error() string { return f.what }

// TooManyRequests is Telegram's flood wait, retry_after included, so a bot that
// honours it can be watched doing so.
func TooManyRequests(retryAfter time.Duration) Fault {
	seconds := int(retryAfter.Round(time.Second).Seconds())
	return refusal(&apiError{
		Code:        http.StatusTooManyRequests,
		Description: fmt.Sprintf("Too Many Requests: retry after %d", seconds),
		RetryAfter:  seconds,
	})
}

// Blocked is the user shutting the bot out, which a bot only ever learns from
// the next thing it tries to send them.
func Blocked() Fault {
	return refusal(&apiError{
		Code:        http.StatusForbidden,
		Description: "Forbidden: bot was blocked by the user",
	})
}

func ServerError() Fault {
	return refusal(&apiError{Code: http.StatusInternalServerError, Description: "Internal Server Error"})
}

func refusal(e *apiError) Fault {
	return Fault{e.Error(), func(w http.ResponseWriter) { writeError(w, e) }}
}

// Malformed answers with a body no client can decode, the way a proxy that
// truncates a reply does.
func Malformed() Fault {
	return Fault{"kitchen: a malformed reply", func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":`)
	}}
}

// Timeout drops the connection rather than holding it open, so the bot meets the
// failure at once instead of waiting out its own client timeout.
func Timeout() Fault {
	return Fault{"kitchen: a dropped connection", func(http.ResponseWriter) {
		panic(http.ErrAbortHandler)
	}}
}

// Fail refuses every call the matchers pick out, until the failure is cleared.
func (k *Kitchen) Fail(f Fault, ms ...Matcher) *Failure {
	return k.faults.add(&Failure{fault: f, when: All(ms...), left: -1})
}

// FailOnce refuses a single call, the shape of a fault a retry recovers from.
func (k *Kitchen) FailOnce(f Fault, ms ...Matcher) *Failure {
	return k.faults.add(&Failure{fault: f, when: All(ms...), left: 1})
}

// FailAfter lets n matching calls through and refuses the rest, for a bot that
// breaks only once it is already mid-conversation.
func (k *Kitchen) FailAfter(n int, f Fault, ms ...Matcher) *Failure {
	return k.faults.add(&Failure{fault: f, when: All(ms...), skip: n, left: -1})
}

func (k *Kitchen) ClearFaults() { k.faults.clear() }

// Failure is a fault standing in the pipeline, and the calls it is waiting for.
type Failure struct {
	fault Fault
	when  Matcher

	mu   sync.Mutex
	skip int
	left int // refusals remaining; negative stands until cleared
	gone bool
}

// Clear takes the fault down, so the calls it was refusing go through again.
func (f *Failure) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gone = true
}

func (f *Failure) fires(s subject) bool {
	if !f.when.match(s) {
		return false
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.gone || f.left == 0 {
		return false
	}
	if f.skip > 0 {
		f.skip--
		return false
	}
	if f.left > 0 {
		f.left--
	}
	return true
}

type faultStore struct {
	mu       sync.Mutex
	standing []*Failure
}

func newFaultStore() *faultStore { return &faultStore{} }

func (s *faultStore) add(f *Failure) *Failure {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.standing = append(s.standing, f)
	return f
}

// The first standing fault that wants the call gets it, so a narrow fault laid
// down early is not shadowed by a broad one laid down later.
func (s *faultStore) pick(c Call) (Fault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.standing) == 0 {
		return Fault{}, false
	}
	call := c.subject()
	for _, f := range s.standing {
		if f.fires(call) {
			return f.fault, true
		}
	}
	return Fault{}, false
}

func (s *faultStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.standing = nil
}
