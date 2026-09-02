package kitchen

import (
	"net/http"
	"strings"
	"testing"
)

func TestRoute(t *testing.T) {
	cases := []struct {
		path   string
		token  string
		method string
		ok     bool
	}{
		{path: "/bot123:ABC/getMe", token: "123:ABC", method: "getMe", ok: true},
		{path: "/bot123:ABC/", ok: false},
		{path: "/bot/getMe", ok: false},
		{path: "/123:ABC/getMe", ok: false},
	}
	for _, c := range cases {
		token, method, ok := route(c.path)
		if ok != c.ok || token != c.token || method != c.method {
			t.Errorf("route(%q) = %q, %q, %v; want %q, %q, %v", c.path, token, method, ok, c.token, c.method, c.ok)
		}
	}
}

func TestServeRejectsUnknownToken(t *testing.T) {
	k := New(t)
	reply := callRaw(t, k.APIURL()+"/botwrong-token/getMe", "application/json", strings.NewReader("{}"))
	if reply.OK || reply.status != http.StatusUnauthorized || reply.ErrorCode != http.StatusUnauthorized {
		t.Errorf("reply = %+v, want an unauthorized error", reply)
	}
}

func TestServeReportsUnsupportedMethod(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	defer tb.close()

	wake := k.activity.watch()
	reply := callJSON(t, k, "sendDice", `{"chat_id":1}`)
	if reply.OK || reply.ErrorCode != http.StatusNotFound {
		t.Errorf("reply = %+v, want a not-found error", reply)
	}

	// A refused call still wakes waiters, so a test fails on its own assertion
	// rather than on a timeout.
	select {
	case <-wake:
	default:
		t.Error("the refused call left waiters asleep")
	}

	// However often a bot asks for it, the gap is reported once, so a bot that
	// polls cannot bury it under thousands of copies.
	for range 3 {
		callJSON(t, k, "sendDice", `{"chat_id":1}`)
	}
	errs := tb.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "sendDice") {
		t.Errorf("reported errors = %v, want one naming the missing method", errs)
	}

	callJSON(t, k, "sendPoll", `{"chat_id":1}`)
	if errs := tb.errors(); len(errs) != 2 {
		t.Errorf("reported errors = %v, want a second method reported on its own", errs)
	}
}

// A bot that leaves chat_id unset has to fail here rather than in production.
func TestServeRefusesAnUnsetChatID(t *testing.T) {
	k := New(t)

	reply := callJSON(t, k, "sendMessage", `{"chat_id":0,"text":"hello"}`)
	if reply.OK || reply.ErrorCode != http.StatusBadRequest {
		t.Errorf("reply = %+v, want a bad-request error", reply)
	}
	if log := k.History(0); len(log) != 0 {
		t.Errorf("chat 0 holds %d messages, want the refused send to have left none", len(log))
	}
}

func TestServeReadsBothEncodings(t *testing.T) {
	k := New(t)

	callForm(t, k, "setWebhook", map[string]string{
		"url":             "https://form.example",
		"allowed_updates": `["message"]`,
	})
	assertWebhookURL(t, k, "https://form.example", []string{"message"})

	callJSON(t, k, "setWebhook", `{"url":"https://json.example","allowed_updates":["callback_query"]}`)
	assertWebhookURL(t, k, "https://json.example", []string{"callback_query"})
}

// A parameterless call carries a multipart content type with no body at all.
func TestServeAcceptsEmptyBody(t *testing.T) {
	k := New(t)
	reply := callRaw(t, k.methodURL("getMe"), "multipart/form-data; boundary=empty", http.NoBody)
	if !reply.OK {
		t.Fatalf("reply = %+v, want ok", reply)
	}
}

func TestServeRejectsMalformedJSON(t *testing.T) {
	k := New(t)
	reply := callJSON(t, k, "setWebhook", `{"url":`)
	if reply.OK || reply.ErrorCode != http.StatusBadRequest {
		t.Errorf("reply = %+v, want a bad-request error", reply)
	}
}

func TestKitchenStopsOnCleanup(t *testing.T) {
	tb := &recordingTB{}
	k := New(tb)
	tb.close()

	if _, err := http.Get(k.methodURL("getMe")); err == nil {
		t.Error("server still answering after cleanup")
	}
}

func assertWebhookURL(t *testing.T, k *Kitchen, url string, allowed []string) {
	t.Helper()
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.webhook.url != url {
		t.Errorf("webhook url = %q, want %q", k.webhook.url, url)
	}
	if len(k.webhook.allowedUpdates) != len(allowed) || (len(allowed) > 0 && k.webhook.allowedUpdates[0] != allowed[0]) {
		t.Errorf("allowed updates = %v, want %v", k.webhook.allowedUpdates, allowed)
	}
}
