package kitchen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"
)

// recordingTB stands in for *testing.T so the kitchen's own failure reporting
// can be asserted instead of failing the run.
type recordingTB struct {
	mu       sync.Mutex
	errs     []string
	cleanups []func()
}

func (r *recordingTB) Cleanup(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups = append(r.cleanups, f)
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Failed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.errs) > 0
}

func (r *recordingTB) errors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errs...)
}

func (r *recordingTB) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
}

type apiReply struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	status      int
}

func (r apiReply) decode(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.Result, dst); err != nil {
		t.Fatalf("decode result %s: %v", r.Result, err)
	}
}

func callRaw(t *testing.T, url, contentType string, body io.Reader) apiReply {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	defer resp.Body.Close()

	reply := apiReply{status: resp.StatusCode}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return reply
}

func callJSON(t *testing.T, k *Kitchen, method, body string) apiReply {
	t.Helper()
	return callRaw(t, k.methodURL(method), "application/json", strings.NewReader(body))
}

func callForm(t *testing.T, k *Kitchen, method string, fields map[string]string) apiReply {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			t.Fatalf("write field %s: %v", name, err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	return callRaw(t, k.methodURL(method), form.FormDataContentType(), &body)
}

func (k *Kitchen) methodURL(method string) string {
	return k.APIURL() + "/bot" + k.token + "/" + method
}

func newClient(t *testing.T, k *Kitchen) *bot.Bot {
	t.Helper()
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()))
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}
	return b
}
