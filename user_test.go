package kitchen

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestUserTextReachesTheBot(t *testing.T) {
	k := New(t)
	k.DeliverTo(syncBot(t, k, echoHandler).ProcessUpdate)

	user := k.User(7, WithFullName("Ali", "Rezaei"), WithUsername("ali"), WithLanguage("fa"))
	user.Send("سلام")

	log := k.world.history(user.ChatID())
	if len(log) != 2 {
		t.Fatalf("history = %+v, want the user's message and the reply", log)
	}

	sent := log[0]
	if sent.Text != "سلام" || sent.From == nil || sent.From.Username != "ali" {
		t.Errorf("sent = %+v, want the user's own message", sent)
	}
	if sent.From.LanguageCode != "fa" {
		t.Errorf("language = %q, want the configured one", sent.From.LanguageCode)
	}
	if sent.Chat.ID != 7 || sent.Chat.FirstName != "Ali" || sent.Chat.Username != "ali" {
		t.Errorf("chat = %+v, want the user's private chat", sent.Chat)
	}
	if log[1].Text != "echo: سلام" {
		t.Errorf("reply = %q, want the echo", log[1].Text)
	}
}

func TestSendCommandCarriesEntity(t *testing.T) {
	k := New(t)
	var got *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = u.Message })

	k.User(7).SendCommand("settings", "lang", "fa")

	if got.Text != "/settings lang fa" {
		t.Fatalf("text = %q, want the command with its arguments", got.Text)
	}
	want := models.MessageEntity{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: 9}
	if len(got.Entities) != 1 || got.Entities[0] != want {
		t.Errorf("entities = %+v, want one covering %q", got.Entities, "/settings")
	}
}

func TestSendCommandAcceptsALeadingSlash(t *testing.T) {
	k := New(t)
	var got *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = u.Message })

	k.User(7).SendCommand("/start")

	if got.Text != "/start" || got.Entities[0].Length != 6 {
		t.Errorf("message = %+v, want a single /start command", got)
	}
}

// A user typing a command into their client gets the same message as one asking
// for it by name, so a test may write either.
func TestTypedCommandIsRecognized(t *testing.T) {
	cases := map[string]int{
		"/start":            6,
		"/start deep-link":  6,
		"/settings@the_bot": 17,
	}
	for text, want := range cases {
		got := commandEntities(text)
		if len(got) != 1 || got[0].Length != want || got[0].Type != models.MessageEntityTypeBotCommand {
			t.Errorf("commandEntities(%q) = %+v, want one of length %d", text, got, want)
		}
	}

	for _, text := range []string{"hello", "and/or", "/ start", "/"} {
		if got := commandEntities(text); got != nil {
			t.Errorf("commandEntities(%q) = %+v, want no command", text, got)
		}
	}
}

// Telegram counts entity lengths in UTF-16 code units, not bytes or runes.
func TestEntityLengthCountsUTF16(t *testing.T) {
	cases := map[string]int{
		"/start": 6,
		"/شروع":  5,
		"/👍":     3,
	}
	for text, want := range cases {
		if got := utf16Len(text); got != want {
			t.Errorf("utf16Len(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestUserOptionsAreAdditive(t *testing.T) {
	k := New(t)
	k.User(7, WithUsername("ali"))
	again := k.User(7, WithLanguage("fa"))

	if again.info.Username != "ali" || again.info.LanguageCode != "fa" {
		t.Errorf("user = %+v, want both settings kept", again.info)
	}
	if first := k.User(7); first != again {
		t.Error("the same id produced a second user")
	}
}

func TestTheBotCannotWriteFirst(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ctx := context.Background()
	send := func(chatID int64) error {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "hello"})
		return err
	}

	ada := k.User(7)
	if err := send(ada.ID()); err == nil || !strings.Contains(err.Error(), "bot can't initiate conversation with a user") {
		t.Errorf("to a user who never wrote: err = %v, want it forbidden", err)
	}

	team := k.Group(-42, "Standup")
	ada.In(team).Send("hi all")
	if err := send(team.ID()); err != nil {
		t.Errorf("to the group: %v", err)
	}
	if err := send(ada.ID()); err == nil {
		t.Error("after speaking only in a group: sent, want it still forbidden")
	}

	ada.SendCommand("start")
	if err := send(ada.ID()); err != nil {
		t.Errorf("after /start: %v", err)
	}
	if err := send(k.User(8, Started()).ID()); err != nil {
		t.Errorf("to a user who started it before the test: %v", err)
	}
	if err := send(424242); err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("to nobody the kitchen knows: err = %v, want chat not found", err)
	}

	grace := k.User(9)
	if _, err := b.CopyMessage(ctx, &bot.CopyMessageParams{
		ChatID: grace.ID(), FromChatID: ada.ID(), MessageID: ada.Screen().ID,
	}); err == nil {
		t.Error("a copy to a user who never wrote was sent, want it forbidden")
	}
	if _, err := b.SendChatAction(ctx, &bot.SendChatActionParams{ChatID: grace.ID(), Action: models.ChatActionTyping}); err == nil {
		t.Error("a chat action to a user who never wrote was sent, want it forbidden")
	}
}

func TestABlockedBotHearsOfItAndIsRefused(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var got updates
	got.collect(k)
	ctx := context.Background()
	send := func(chatID int64) error {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "hello"})
		return err
	}

	ada := k.User(7, Started())
	card, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: ada.ID(), Text: "card"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	ada.BlockBot()
	seen := got.all()
	if len(seen) != 1 || seen[0].MyChatMember == nil {
		t.Fatalf("updates = %+v, want one my_chat_member", seen)
	}
	if blocked := seen[0].MyChatMember; blocked.Chat.ID != ada.ID() || blocked.From.ID != ada.ID() ||
		blocked.OldChatMember.Type != models.ChatMemberTypeMember || blocked.NewChatMember.Type != models.ChatMemberTypeBanned {
		t.Errorf("my_chat_member = %+v, want ada taking the bot from member to kicked", blocked)
	}
	if err := send(ada.ID()); err == nil || !strings.Contains(err.Error(), "bot was blocked by the user") {
		t.Errorf("a send after the block: err = %v, want it forbidden", err)
	}
	if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: ada.ID(), MessageID: card.ID, Text: "edited",
	}); err == nil || !strings.Contains(err.Error(), "bot was blocked by the user") {
		t.Errorf("an edit after the block: err = %v, want it forbidden", err)
	}

	ada.UnblockBot()
	seen = got.all()
	if len(seen) != 2 || seen[1].MyChatMember == nil ||
		seen[1].MyChatMember.OldChatMember.Type != models.ChatMemberTypeBanned ||
		seen[1].MyChatMember.NewChatMember.Type != models.ChatMemberTypeMember {
		t.Errorf("updates = %+v, want the bot taken from kicked back to member", seen)
	}
	if err := send(ada.ID()); err != nil {
		t.Errorf("a send after the unblock: %v", err)
	}

	// Blocking and unblocking is not opening the chat.
	grace := k.User(9)
	grace.BlockBot()
	if err := send(grace.ID()); err == nil || !strings.Contains(err.Error(), "bot was blocked by the user") {
		t.Errorf("to somebody who blocked a bot they never started: err = %v, want blocked", err)
	}
	grace.UnblockBot()
	if err := send(grace.ID()); err == nil || !strings.Contains(err.Error(), "bot can't initiate conversation") {
		t.Errorf("after unblocking a bot never started: err = %v, want it still unable to write first", err)
	}
}

func TestAUserWhoBlockedTheBotCannotReachIt(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	var got updates
	got.collect(k)
	ada := k.User(7, Started())
	said := func(act func()) []string {
		before := len(tb.errors())
		act()
		return tb.errors()[before:]
	}

	ada.BlockBot()
	for _, verb := range []struct {
		name string
		act  func()
	}{
		{"Send", func() { ada.Send("hi") }},
		{"Tap", func() { ada.Tap("OK") }},
		{"Edit", func() { ada.Edit(Message{ID: 1, ChatID: ada.ID()}, "hi") }},
		{"ReactTo", func() { ada.ReactTo(Message{ID: 1, ChatID: ada.ID()}, "👍") }},
		{"Vote", func() { ada.Vote("yes") }},
		{"Pay", func() { ada.Pay() }},
		{"Pick", func() { ada.Pick("first") }},
	} {
		if errs := said(verb.act); len(errs) != 1 || !strings.Contains(errs[0], "blocked the bot, so nothing") {
			t.Errorf("%s: errors = %q, want one saying the user blocked the bot", verb.name, errs)
		}
	}
	if errs := said(ada.BlockBot); len(errs) != 1 || !strings.Contains(errs[0], "already blocked") {
		t.Errorf("blocking twice: errors = %q, want one", errs)
	}
	if seen := got.all(); len(seen) != 1 {
		t.Errorf("updates = %+v, want only the block", seen)
	}

	ada.UnblockBot()
	if errs := said(func() { ada.Send("back") }); len(errs) != 0 {
		t.Errorf("after the unblock: errors = %q, want none", errs)
	}
	if errs := said(ada.UnblockBot); len(errs) != 1 || !strings.Contains(errs[0], "nothing to unblock") {
		t.Errorf("unblocking twice: errors = %q, want one", errs)
	}
	if seen := got.all(); len(seen) != 3 {
		t.Errorf("updates = %+v, want the block, the unblock and the message", seen)
	}
}

func TestAUserDeletesAMessageWithoutTheBotHearing(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	var got updates
	got.collect(k)
	ctx := context.Background()

	ada := k.User(7, Started())
	ada.Send("mine")
	card, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: ada.ID(), Text: "searching"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	ada.Delete(ada.History()[1])
	if seen := got.all(); len(seen) != 1 {
		t.Errorf("updates = %+v, want nothing for the delete", seen)
	}
	if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: ada.ID(), MessageID: card.ID, Text: "still searching",
	}); err == nil || !strings.Contains(err.Error(), "message to edit not found") {
		t.Errorf("editing the deleted card: err = %v, want not found", err)
	}
	if _, err := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID: ada.ID(), MessageID: card.ID,
	}); err == nil || !strings.Contains(err.Error(), "message to delete not found") {
		t.Errorf("deleting the deleted card: err = %v, want not found", err)
	}
	again, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: ada.ID(), Text: "searching"})
	if err != nil {
		t.Fatalf("sending the card again: %v", err)
	}

	ada.Delete(ada.History()[0])
	if history := ada.History(); len(history) != 1 || history[0].ID != again.ID {
		t.Errorf("history = %+v, want only the card sent again", history)
	}
}

func TestAMemberDeletesOnlyWhatTheyMay(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")
	ali, bob := k.User(7).In(team), k.User(9).In(team)
	ali.Send("ali's")
	bob.Send("bob's")
	alis, bobs := team.History()[0], team.History()[1]
	k.User(7).Send("elsewhere")
	elsewhere := k.User(7).History()[0]

	bob.Delete(alis)
	bob.Delete(bobs)
	if history := team.History(); len(history) != 1 || history[0].ID != alis.ID {
		t.Errorf("history = %+v, want bob's own message gone and ali's kept", history)
	}

	ali.Promote(k.User(9), DeleteMessages)
	bob.Delete(elsewhere)
	bob.Delete(alis)
	if history := team.History(); len(history) != 0 {
		t.Errorf("history = %+v, want ali's message gone once bob holds the right", history)
	}
	bob.Delete(alis)

	want := []string{"may not delete message 1", "is in chat 7", "has no message 1 to delete"}
	errs := tb.errors()
	if len(errs) != len(want) {
		t.Fatalf("errors = %q, want %d", errs, len(want))
	}
	for i := range want {
		if !strings.Contains(errs[i], want[i]) {
			t.Errorf("error %d = %q, want it to say %q", i, errs[i], want[i])
		}
	}
}

func syncBot(t *testing.T, k *Kitchen, handler bot.HandlerFunc) *bot.Bot {
	t.Helper()
	b, err := bot.New(k.Token(), bot.WithServerURL(k.APIURL()),
		bot.WithNotAsyncHandlers(), bot.WithDefaultHandler(handler))
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}
	return b
}

func TestSendPhotoOffersTheLargestLast(t *testing.T) {
	k := New(t)
	var got *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = u.Message })

	data := []byte("jpeg-bytes")
	k.User(7).SendPhoto("selfie.jpg", data, "look")

	if len(got.Photo) < 2 {
		t.Fatalf("photo = %+v, want a size ladder", got.Photo)
	}
	largest := got.Photo[len(got.Photo)-1]
	for _, size := range got.Photo[:len(got.Photo)-1] {
		if size.Width >= largest.Width {
			t.Errorf("size %+v is not smaller than the last one %+v", size, largest)
		}
	}
	if got.Caption != "look" {
		t.Errorf("caption = %q, want the one sent", got.Caption)
	}

	file, ok := k.File(largest.FileID)
	if !ok || string(file.Data) != string(data) {
		t.Errorf("file = %+v, %v; want the uploaded bytes addressable by the largest size", file, ok)
	}
}

func TestShareLocation(t *testing.T) {
	k := New(t)
	var got *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = u.Message })

	k.User(7).ShareLocation(35.6892, 51.389)

	if got.Location == nil || got.Location.Latitude != 35.6892 || got.Location.Longitude != 51.389 {
		t.Errorf("location = %+v, want the shared coordinates", got.Location)
	}
}

func TestAUserIsRefusedANegativeID(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	New(tb).User(-42)

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "must be positive") {
		t.Errorf("errors = %v, want one about the id", errs)
	}
}
