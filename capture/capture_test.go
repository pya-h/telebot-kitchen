package capture_test

import (
	"errors"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pya-h/telebot-kitchen/capture"

	// The kitchen's -kitchen.* flags reach every test binary in the module, and
	// this one would refuse them. Importing the toolbox from the test alone
	// keeps `go test ./... -kitchen.stress` working without putting anything in
	// the package a live bot carries.
	_ "github.com/pya-h/telebot-kitchen"
)

func TestARecordingIsOneUpdateALine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.jsonl")
	tape, err := capture.To(path)
	if err != nil {
		t.Fatalf("To: %v", err)
	}
	tape.Add([]byte("{\n  \"update_id\": 1,\n  \"x\": \"a\"\n}"))
	tape.Add([]byte(`{"update_id":2}`))
	tape.Close()

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "{\"update_id\":1,\"x\":\"a\"}\n{\"update_id\":2}\n"
	if string(written) != want {
		t.Errorf("tape = %q, want %q", written, want)
	}
}

// A bot restarted mid-incident keeps what led up to it.
func TestASecondRecordingAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape.jsonl")
	first, _ := capture.To(path)
	first.Add([]byte(`{"update_id":1}`))
	first.Close()

	second, err := capture.To(path)
	if err != nil {
		t.Fatalf("To: %v", err)
	}
	second.Add([]byte(`{"update_id":2}`))
	second.Close()

	written, _ := os.ReadFile(path)
	if lines := strings.Count(string(written), "\n"); lines != 2 {
		t.Errorf("tape = %q, want both runs on it", written)
	}
}

func TestTheWrappedHandlerStillReadsWhatItWasPosted(t *testing.T) {
	var kept strings.Builder
	tape := capture.Into(nopCloser{&kept})

	var got string
	wrapped := tape.Webhook(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		w.WriteHeader(http.StatusAccepted)
	}), func(err error) { t.Errorf("capture: %v", err) })

	update := `{"update_id":1,"message":{"text":"hi"}}`
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(update)))

	if got != update {
		t.Errorf("handler read %q, want the update untouched", got)
	}
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want the handler's own", rec.Code)
	}
	if kept.String() != update+"\n" {
		t.Errorf("tape = %q, want the same update recorded", kept.String())
	}
}

func TestAFailedRecordingStillLetsTheUpdateThrough(t *testing.T) {
	tape := capture.Into(broken{})

	served := false
	var reported error
	wrapped := tape.Webhook(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }),
		func(err error) { reported = err },
	)
	wrapped.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"update_id":1}`)))

	if !served {
		t.Error("the update was dropped, want the bot to see it anyway")
	}
	if reported == nil {
		t.Error("nothing was reported, want the failure named")
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type broken struct{}

func (broken) Write([]byte) (int, error) { return 0, errors.New("disk full") }
func (broken) Close() error              { return nil }

// The whole reason this package stands apart: a bot in production imports it,
// and must not be handed the kitchen's test flags or a server it has no use
// for. Nothing here may reach back into the toolbox.
func TestNothingHereReachesBackIntoTheKitchen(t *testing.T) {
	unwelcome := []string{"telebot-kitchen", "flag", "testing", "httptest"}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imported := range file.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			for _, no := range unwelcome {
				if strings.Contains(path, no) {
					t.Errorf("%s imports %s; keep this package something a live bot can carry", name, path)
				}
			}
		}
	}
}
