// Package capture writes the updates a live bot receives to a file, so an
// incident in production can be handed back to a test.
//
// It is deliberately apart from the kitchen itself: a bot in production should
// not import a package that registers test flags and stands up servers. This
// one is standard library only, and the file it writes is Telegram's own JSON.
package capture

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
)

// A Session is a bot's updates on their way to a file, one JSON object a line.
// Each is written as it arrives, so a bot that dies mid-incident still leaves
// behind what led up to it.
type Session struct {
	mu   sync.Mutex
	to   io.WriteCloser
	fail error
}

func To(path string) (*Session, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Session{to: file}, nil
}

// Into records somewhere other than a file, which a test of the recording
// itself needs and a bot shipping its updates elsewhere may want.
func Into(w io.WriteCloser) *Session { return &Session{to: w} }

func (s *Session) Add(update []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fail != nil {
		return s.fail
	}
	// Compacted, because a line of the file has to be one update.
	var line bytes.Buffer
	if err := json.Compact(&line, update); err != nil {
		return err
	}
	line.WriteByte('\n')
	_, s.fail = s.to.Write(line.Bytes())
	return s.fail
}

// Webhook records every update posted to the bot and passes it on unchanged.
// For a bot behind a webhook this is the whole change: wrap the handler.
//
// A recording that fails does not fail the bot; onError hears about it, and a
// nil onError means the update goes through and nothing is said.
func (s *Session) Webhook(next http.Handler, onError func(error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		update, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			if onError != nil {
				onError(err)
			}
			http.Error(w, "cannot read update", http.StatusBadRequest)
			return
		}
		if err := s.Add(update); err != nil && onError != nil {
			onError(err)
		}
		// The handler still has to be able to read what it was posted.
		r.Body = io.NopCloser(bytes.NewReader(update))
		r.ContentLength = int64(len(update))
		next.ServeHTTP(w, r)
	})
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.to.Close()
}
