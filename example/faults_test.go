package example

import (
	"strings"
	"testing"
	"time"

	kitchen "github.com/pya-h/telebot-kitchen"
)

// Refusing a call turns a robustness gap into a failing test: this bot does not
// retry, so a flood wait costs the user their menu outright.
func TestAFloodWaitCostsTheMenu(t *testing.T) {
	k, ada := start(t)
	k.FailOnce(kitchen.TooManyRequests(time.Second), kitchen.Method("sendMessage"))

	ada.SendCommand("start")

	refused := k.Expect(kitchen.Method("sendMessage"))
	if !strings.Contains(refused.Error, "Too Many Requests") {
		t.Errorf("call = %+v, want the flood wait recorded against the attempt", refused)
	}
	ada.ExpectNothingMore()
}
