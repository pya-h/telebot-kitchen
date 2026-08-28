package kitchen

import "sync"

type CallbackAnswer struct {
	QueryID   string
	Text      string
	ShowAlert bool
	URL       string
	CacheTime int
}

type callbackLog struct {
	mu      sync.RWMutex
	answers []CallbackAnswer
	byQuery map[string]CallbackAnswer
}

func newCallbackLog() *callbackLog { return &callbackLog{byQuery: map[string]CallbackAnswer{}} }

func (l *callbackLog) record(a CallbackAnswer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.answers = append(l.answers, a)
	l.byQuery[a.QueryID] = a
}

func (l *callbackLog) byID(queryID string) (CallbackAnswer, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a, ok := l.byQuery[queryID]
	return a, ok
}

func (l *callbackLog) all() []CallbackAnswer {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]CallbackAnswer(nil), l.answers...)
}
