package kitchen

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

func standalone(t *testing.T, opts ...Option) (*Kitchen, *Log) {
	t.Helper()
	said := Logging(io.Discard)
	k := New(said, append([]Option{WithWaitTimeout(time.Second)}, opts...)...)
	t.Cleanup(said.Close)
	return k, said
}

func ask(t *testing.T, k *Kitchen, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, k.APIURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()

	var answer map[string]any
	if err := json.NewDecoder(res.Body).Decode(&answer); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return res.StatusCode, answer
}

func TestTheKitchenServesOnAnAddressItWasGiven(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick a port: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	k, _ := standalone(t, WithAddress(addr))
	if got := k.APIURL(); got != "http://"+addr {
		t.Errorf("serving on %s, want the address it was given (%s)", got, addr)
	}
}

func TestAnAddressAlreadyTakenIsReported(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer taken.Close()

	said := Logging(io.Discard)
	New(said, WithAddress(taken.Addr().String()))
	defer said.Close()

	if !said.Failed() {
		t.Error("the clash went unmentioned, want the kitchen to say the port was taken")
	}
}

// The control surface drives a conversation from outside the process, which is
// the whole point of serving on a real port.
func TestTheControlSurfaceDrivesAConversation(t *testing.T) {
	k, _ := standalone(t)
	k.DeliverTo(func(ctx context.Context, u *models.Update) {})

	ask(t, k, http.MethodPost, "/kitchen/user", `{"id":7,"first_name":"Ada","username":"ada"}`)
	ask(t, k, http.MethodPost, "/kitchen/chat", `{"id":-1001,"type":"supergroup","title":"Standup"}`)
	ask(t, k, http.MethodPost, "/kitchen/join", `{"user":7,"chat":-1001}`)
	ask(t, k, http.MethodPost, "/kitchen/send", `{"user":7,"chat":-1001,"text":"hi"}`)

	code, answer := ask(t, k, http.MethodGet, "/kitchen/transcript?chat=-1001", "")
	if code != http.StatusOK || answer["ok"] != true {
		t.Fatalf("transcript answered %d %v", code, answer)
	}
	if got, _ := answer["result"].(string); !strings.Contains(got, "**Ada:** hi") {
		t.Errorf("transcript = %q, want what the control surface put there", got)
	}
}

// Nothing can fail a run that is not a test, so what the kitchen would have
// failed comes back with the reply instead of only being written down.
func TestTheControlSurfaceAnswersWithWhatWentWrong(t *testing.T) {
	k, _ := standalone(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	code, answer := ask(t, k, http.MethodPost, "/kitchen/chat", `{"id":-1,"type":"parliament"}`)
	if code != http.StatusBadRequest || answer["ok"] != false {
		t.Fatalf("answered %d %v, want it refused", code, answer)
	}
	wrong, _ := answer["errors"].([]any)
	if len(wrong) != 1 || !strings.Contains(wrong[0].(string), "parliament") {
		t.Errorf("errors = %v, want the kind named", wrong)
	}
}

func TestAnUnknownControlVerbIsNotFound(t *testing.T) {
	k, _ := standalone(t)
	res, err := http.Post(k.APIURL()+"/kitchen/juggle", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
}

// Serving a bot outside the process means posting to the webhook it registered
// over a real connection, and queueing for getUpdates when it registered none.
func TestDeliveryOverHTTPFollowsTheBotsOwnChoice(t *testing.T) {
	k, _ := standalone(t)
	k.DeliverOverHTTP()

	var mu sync.Mutex
	var posted []models.Update
	var secret string
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var u models.Update
		json.Unmarshal(body, &u)
		mu.Lock()
		defer mu.Unlock()
		posted, secret = append(posted, u), r.Header.Get(secretTokenHeader)
	}))
	defer bot.Close()

	// Nothing registered yet, so the update waits for a poll.
	k.User(7).Send("first")
	if held := k.updates.held(); held != 1 {
		t.Errorf("queue holds %d, want the update waiting for getUpdates", held)
	}

	callJSON(t, k, "setWebhook", `{"url":"`+bot.URL+`/hook","secret_token":"s3cret"}`)
	k.User(7).Send("second")

	mu.Lock()
	defer mu.Unlock()
	if len(posted) != 1 || posted[0].Message == nil || posted[0].Message.Text != "second" {
		t.Errorf("posted = %+v, want the update sent to the registered webhook", posted)
	}
	if secret != "s3cret" {
		t.Errorf("secret = %q, want the one the bot registered", secret)
	}
}

func TestAWebhookThatCannotBeReachedIsReported(t *testing.T) {
	k, said := standalone(t)
	k.DeliverOverHTTP()
	callJSON(t, k, "setWebhook", `{"url":"http://127.0.0.1:1/hook"}`)

	k.User(7).Send("hi")
	if !said.Failed() {
		t.Error("the failed post went unmentioned")
	}
}

// A bot whose entry point takes its own library's type is wired without the
// kitchen ever importing that library.
func TestDeliveryAsRawJSON(t *testing.T) {
	k, _ := standalone(t)

	var mu sync.Mutex
	var seen []string
	k.DeliverToJSON(func(_ context.Context, update []byte) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, string(bytes.TrimSpace(update)))
	})

	k.User(7).Send("hi")

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || !strings.Contains(seen[0], `"text":"hi"`) {
		t.Errorf("delivered %v, want the update as Telegram's own JSON", seen)
	}
}
