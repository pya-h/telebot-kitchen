package kitchen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
)

const wireTimeout = 10 * time.Second

type Log struct {
	mu       sync.Mutex
	to       io.Writer
	said     []string
	cleanups []func()
}

func Logging(to io.Writer) *Log { return &Log{to: to} }

func (l *Log) Cleanup(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanups = append(l.cleanups, f)
}

func (l *Log) Errorf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	said := fmt.Sprintf(format, args...)
	l.said = append(l.said, said)
	fmt.Fprintln(l.to, said)
}

func (l *Log) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.to, fmt.Sprintf(format, args...))
}

func (l *Log) Failed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.said) > 0
}

func (l *Log) Close() {
	l.mu.Lock()
	cleanups := l.cleanups
	l.cleanups = nil
	l.mu.Unlock()

	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
}

func (l *Log) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.said)
}

func (l *Log) since(mark int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if mark >= len(l.said) {
		return nil
	}
	return append([]string(nil), l.said[mark:]...)
}

func (k *Kitchen) DeliverOverHTTP() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.wire, k.process, k.hook, k.polling = true, nil, nil, false
}

func (k *Kitchen) send(registered webhook, u models.Update) {
	body, err := json.Marshal(u)
	if err != nil {
		k.tb.Errorf("kitchen: encode update %d: %v", u.ID, err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, registered.url, bytes.NewReader(body))
	if err != nil {
		k.tb.Errorf("kitchen: the registered webhook %q cannot be reached: %v", registered.url, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if registered.secretToken != "" {
		req.Header.Set(secretTokenHeader, registered.secretToken)
	}

	res, err := (&http.Client{Timeout: wireTimeout}).Do(req)
	if err != nil {
		k.tb.Errorf("kitchen: post update %d to %s: %v", u.ID, registered.url, err)
		return
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	if res.StatusCode >= http.StatusBadRequest {
		k.tb.Errorf("kitchen: the bot answered update %d with %s", u.ID, res.Status)
	}
}
