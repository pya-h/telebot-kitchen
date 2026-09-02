package kitchen

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestCallsAreRecordedInOrder(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	mustSend(t, b, "one")
	mustSend(t, b, "two")

	// The record is everything the bot sent, the library's own handshake included.
	calls := k.Calls()
	if !calls.Has(Method("getMe")) {
		t.Errorf("calls = %v, want the bootstrap call on the record too", calls)
	}

	sends := calls.Matching(Method("sendMessage"))
	if len(sends) != 2 || sends[0].Text() != "one" || sends[1].Text() != "two" {
		t.Fatalf("sends = %v, want both, in the order the bot made them", sends)
	}
	if sends[0].ChatID != testChatID {
		t.Errorf("call = %+v, want the chat it named", sends[0])
	}
}

// A call the bot got wrong is still something the bot did, so it stays on the
// record with the reason it was refused.
func TestRejectedCallsStayOnTheRecord(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	if _, err := b.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID:    testChatID,
		MessageID: 99,
		Text:      "nowhere",
	}); err == nil {
		t.Fatal("EditMessageText on a missing message: want an error")
	}

	call, ok := k.Calls().Last(Method("editMessageText"))
	if !ok || !strings.Contains(call.Error, "message to edit not found") {
		t.Errorf("call = %+v, %v; want the rejection recorded", call, ok)
	}
}

func TestMatchersSelectCalls(t *testing.T) {
	k := New(t)
	k.DeliverTo(menuBot(t, k, languageMenu...).ProcessUpdate)

	user := k.User(7)
	user.Send("hi")
	user.Tap("English")

	calls := k.Calls()
	if n := calls.Count(Method("sendMessage"), ToUser(user)); n != 2 {
		t.Errorf("sends to the user = %d, want the menu and the answer", n)
	}
	if !calls.Has(HasButton("lang:fa")) {
		t.Errorf("calls = %v, want the menu keyboard found by callback data", calls)
	}
	if !calls.Has(Method("answerCallbackQuery")) {
		t.Errorf("calls = %v, want the tap acknowledged", calls)
	}

	last, ok := calls.Last(TextContains("tapped"))
	if !ok || last.Text() != "tapped: lang:en" {
		t.Errorf("last = %+v, %v; want the answer to the tap", last, ok)
	}
	if n := calls.Count(Any(Method("deleteMessage"), Method("sendPhoto"))); n != 0 {
		t.Errorf("count = %d, want nothing for methods the bot never called", n)
	}
}

func TestMatchersDescribeWhatTheyWant(t *testing.T) {
	want := All(Method("sendMessage"), ToChat(7), TextIs("hi")).String()
	if want != `method sendMessage and to chat 7 and text "hi"` {
		t.Errorf("description = %q, want a readable sentence", want)
	}
}

func TestParamReachesAnythingUnmatched(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID:    testChatID,
		Text:      "*bold*",
		ParseMode: models.ParseModeMarkdown,
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if !k.Calls().Has(Param("parse_mode", "MarkdownV2")) {
		t.Errorf("calls = %v, want the parse mode the bot sent", k.Calls())
	}
}
