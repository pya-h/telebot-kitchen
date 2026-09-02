package kitchen

import (
	"strconv"
	"sync"
)

// Call is one Bot API method the bot invoked, with the parameters it sent. A
// call the kitchen rejected is on the record too, carrying the reason, so a
// test can assert on what the bot got wrong as readily as on what it got right.
type Call struct {
	Method string
	ChatID int64
	Params map[string]string
	Error  string
}

// Text is what the call would put on screen, whichever field carried it.
func (c Call) Text() string {
	if text := c.Params["text"]; text != "" {
		return text
	}
	return c.Params["caption"]
}

func (c Call) Keyboard() [][]Button {
	markup, err := params(c.Params).markup()
	if err != nil {
		return nil
	}
	return buttonsOf(markup)
}

func (c Call) String() string {
	s := c.Method
	if c.ChatID != 0 {
		s += " to chat " + strconv.FormatInt(c.ChatID, 10)
	}
	if text := c.Text(); text != "" {
		s += " " + strconv.Quote(text)
	}
	if c.Error != "" {
		s += " (rejected: " + c.Error + ")"
	}
	return s
}

// Calls is the record in the order the bot made them.
type Calls []Call

func (calls Calls) Matching(ms ...Matcher) Calls {
	want := All(ms...)
	var found Calls
	for _, c := range calls {
		if want.match(c) {
			found = append(found, c)
		}
	}
	return found
}

func (calls Calls) Count(ms ...Matcher) int { return len(calls.Matching(ms...)) }

func (calls Calls) Has(ms ...Matcher) bool { return calls.Count(ms...) > 0 }

func (calls Calls) First(ms ...Matcher) (Call, bool) {
	found := calls.Matching(ms...)
	if len(found) == 0 {
		return Call{}, false
	}
	return found[0], true
}

func (calls Calls) Last(ms ...Matcher) (Call, bool) {
	found := calls.Matching(ms...)
	if len(found) == 0 {
		return Call{}, false
	}
	return found[len(found)-1], true
}

type recorder struct {
	mu    sync.Mutex
	calls Calls
}

func newRecorder() *recorder { return &recorder{} }

func (r *recorder) record(c Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

func (r *recorder) all() Calls {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append(Calls(nil), r.calls...)
}

// Calls returns every Bot API call the bot has made so far.
func (k *Kitchen) Calls() Calls { return k.calls.all() }

func newCall(method string, p params, err error) Call {
	c := Call{Method: method, Params: p}
	if id, ok := p["chat_id"]; ok {
		c.ChatID, _ = strconv.ParseInt(id, 10, 64)
	}
	if err != nil {
		c.Error = err.Error()
	}
	return c
}
