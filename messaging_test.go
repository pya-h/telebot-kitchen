package kitchen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const testChatID = 4242

var testKeyboard = &models.InlineKeyboardMarkup{
	InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "Yes", CallbackData: "yes"},
		{Text: "No", CallbackData: "no"},
	}},
}

func TestSendMessageAppendsToChat(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	sent, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID:      testChatID,
		Text:        "pick one",
		ReplyMarkup: testKeyboard,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if sent.ID != 1 || sent.Chat.ID != testChatID || sent.Text != "pick one" {
		t.Errorf("sent = %+v, want the first message of the chat", sent)
	}
	if sent.From == nil || !sent.From.IsBot {
		t.Errorf("from = %+v, want the kitchen's bot", sent.From)
	}
	if sent.Date != int(k.Clock().Now().Unix()) {
		t.Errorf("date = %d, want the kitchen clock %d", sent.Date, k.Clock().Now().Unix())
	}
	if !sameMarkup(sent.ReplyMarkup, testKeyboard) {
		t.Errorf("keyboard = %+v, want the one sent", sent.ReplyMarkup)
	}

	if log := k.world.history(testChatID); len(log) != 1 {
		t.Errorf("history has %d messages, want 1", len(log))
	}
}

func TestSendMessageRejectsEmptyText(t *testing.T) {
	k := New(t)
	reply := callJSON(t, k, "sendMessage", `{"chat_id":1,"text":""}`)
	if reply.OK || !strings.Contains(reply.Description, "text is empty") {
		t.Errorf("reply = %+v, want an empty-text error", reply)
	}
}

func TestSendPhotoRetainsUpload(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	want := []byte("jpeg-bytes")
	sent, err := b.SendPhoto(context.Background(), &bot.SendPhotoParams{
		ChatID:  testChatID,
		Photo:   &models.InputFileUpload{Filename: "face.jpg", Data: bytes.NewReader(want)},
		Caption: "say cheese",
	})
	if err != nil {
		t.Fatalf("SendPhoto: %v", err)
	}

	if len(sent.Photo) == 0 {
		t.Fatalf("sent = %+v, want a photo size set", sent)
	}
	if sent.Caption != "say cheese" {
		t.Errorf("caption = %q, want the one sent", sent.Caption)
	}

	file, ok := k.File(sent.Photo[0].FileID)
	if !ok {
		t.Fatalf("file %q was not retained", sent.Photo[0].FileID)
	}
	if file.Name != "face.jpg" || !bytes.Equal(file.Data, want) {
		t.Errorf("file = %+v, want the uploaded bytes under their filename", file)
	}
}

// A file id from an earlier message is re-sent as a plain string, not bytes.
func TestSendPhotoAcceptsExistingFileID(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	sent, err := b.SendPhoto(context.Background(), &bot.SendPhotoParams{
		ChatID: testChatID,
		Photo:  &models.InputFileString{Data: "file-from-elsewhere"},
	})
	if err != nil {
		t.Fatalf("SendPhoto: %v", err)
	}
	if len(sent.Photo) == 0 || sent.Photo[0].FileID != "file-from-elsewhere" {
		t.Errorf("photo = %+v, want the file id it was sent with", sent.Photo)
	}
}

func TestEditMessageTextInPlace(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	sent := mustSend(t, b, "before")

	edited, err := b.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID:    testChatID,
		MessageID: sent.ID,
		Text:      "after",
	})
	if err != nil {
		t.Fatalf("EditMessageText: %v", err)
	}

	if edited.ID != sent.ID || edited.Text != "after" {
		t.Errorf("edited = %+v, want the same message with new text", edited)
	}
	if edited.EditDate == 0 {
		t.Error("edit date was not stamped")
	}
	// The keyboard is dropped when an edit omits it, as Telegram does.
	if edited.ReplyMarkup != nil {
		t.Errorf("keyboard = %+v, want it dropped", edited.ReplyMarkup)
	}
	if log := k.world.history(testChatID); len(log) != 1 || log[0].Text != "after" {
		t.Errorf("history = %+v, want the one message edited in place", log)
	}
}

func TestEditRejectsIdenticalContent(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	sent := mustSend(t, b, "same")

	_, err := b.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID:    testChatID,
		MessageID: sent.ID,
		Text:      "same",
	})
	if !errors.Is(err, bot.ErrorBadRequest) || !strings.Contains(err.Error(), "message is not modified") {
		t.Errorf("err = %v, want a not-modified error", err)
	}
}

func TestEditMessageCaptionInPlace(t *testing.T) {
	b := newClient(t, New(t))

	sent, err := b.SendPhoto(context.Background(), &bot.SendPhotoParams{
		ChatID:  testChatID,
		Photo:   &models.InputFileString{Data: "file-1"},
		Caption: "before",
	})
	if err != nil {
		t.Fatalf("SendPhoto: %v", err)
	}

	edited, err := b.EditMessageCaption(context.Background(), &bot.EditMessageCaptionParams{
		ChatID:    testChatID,
		MessageID: sent.ID,
		Caption:   "after",
	})
	if err != nil {
		t.Fatalf("EditMessageCaption: %v", err)
	}
	if edited.Caption != "after" || len(edited.Photo) == 0 {
		t.Errorf("edited = %+v, want a new caption on the same photo", edited)
	}
}

func TestEditMessageReplyMarkupKeepsText(t *testing.T) {
	b := newClient(t, New(t))
	sent := mustSend(t, b, "menu")

	edited, err := b.EditMessageReplyMarkup(context.Background(), &bot.EditMessageReplyMarkupParams{
		ChatID:      testChatID,
		MessageID:   sent.ID,
		ReplyMarkup: testKeyboard,
	})
	if err != nil {
		t.Fatalf("EditMessageReplyMarkup: %v", err)
	}
	if edited.Text != "menu" || !sameMarkup(edited.ReplyMarkup, testKeyboard) {
		t.Errorf("edited = %+v, want the text kept and the keyboard replaced", edited)
	}

	_, err = b.EditMessageReplyMarkup(context.Background(), &bot.EditMessageReplyMarkupParams{
		ChatID:      testChatID,
		MessageID:   sent.ID,
		ReplyMarkup: testKeyboard,
	})
	if !strings.Contains(err.Error(), "message is not modified") {
		t.Errorf("err = %v, want a not-modified error for the same keyboard", err)
	}
}

func TestEditTextRejectsAPhotoMessage(t *testing.T) {
	b := newClient(t, New(t))

	sent, err := b.SendPhoto(context.Background(), &bot.SendPhotoParams{
		ChatID:  testChatID,
		Photo:   &models.InputFileString{Data: "file-1"},
		Caption: "before",
	})
	if err != nil {
		t.Fatalf("SendPhoto: %v", err)
	}

	_, err = b.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID:    testChatID,
		MessageID: sent.ID,
		Text:      "after",
	})
	if err == nil || !strings.Contains(err.Error(), "no text in the message to edit") {
		t.Errorf("err = %v, want a photo message to refuse a text edit", err)
	}
}

func TestEditCaptionRejectsATextMessage(t *testing.T) {
	b := newClient(t, New(t))
	sent := mustSend(t, b, "hello")

	_, err := b.EditMessageCaption(context.Background(), &bot.EditMessageCaptionParams{
		ChatID:    testChatID,
		MessageID: sent.ID,
		Caption:   "after",
	})
	if err == nil || !strings.Contains(err.Error(), "no caption in the message to edit") {
		t.Errorf("err = %v, want a text message to refuse a caption edit", err)
	}
}

func TestEditRejectsAUsersMessage(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})

	user := k.User(testChatID)
	user.Send("mine")

	reply := callJSON(t, k, "editMessageText",
		fmt.Sprintf(`{"chat_id":%d,"message_id":%d,"text":"yours"}`, testChatID, user.Screen().ID))
	if reply.OK || !strings.Contains(reply.Description, "message can't be edited") {
		t.Errorf("reply = %+v, want the bot refused a message it did not send", reply)
	}
}

func TestEditMissingMessage(t *testing.T) {
	k := New(t)
	reply := callJSON(t, k, "editMessageText", `{"chat_id":1,"message_id":7,"text":"hi"}`)
	if reply.OK || !strings.Contains(reply.Description, "message to edit not found") {
		t.Errorf("reply = %+v, want a not-found error", reply)
	}
}

func TestDeleteMessageRemovesFromLog(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	first := mustSend(t, b, "one")
	mustSend(t, b, "two")

	ok, err := b.DeleteMessage(context.Background(), &bot.DeleteMessageParams{
		ChatID:    testChatID,
		MessageID: first.ID,
	})
	if err != nil || !ok {
		t.Fatalf("DeleteMessage = %v, %v; want true", ok, err)
	}

	log := k.world.history(testChatID)
	if len(log) != 1 || log[0].Text != "two" {
		t.Errorf("history = %+v, want only the surviving message", log)
	}

	// Ids are never reused, so a later send must not fall back into the gap.
	if next := mustSend(t, b, "three"); next.ID != 3 {
		t.Errorf("next id = %d, want 3", next.ID)
	}

	if _, err := b.DeleteMessage(context.Background(), &bot.DeleteMessageParams{
		ChatID:    testChatID,
		MessageID: first.ID,
	}); !strings.Contains(err.Error(), "message to delete not found") {
		t.Errorf("err = %v, want a not-found error on the second delete", err)
	}
}

func TestAnswerCallbackQueryRecordsAlert(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	if _, err := b.AnswerCallbackQuery(context.Background(), &bot.AnswerCallbackQueryParams{
		CallbackQueryID: "q-1",
		Text:            "saved",
		ShowAlert:       true,
		CacheTime:       30,
	}); err != nil {
		t.Fatalf("AnswerCallbackQuery: %v", err)
	}

	answer, ok := k.CallbackAnswer("q-1")
	if !ok {
		t.Fatalf("answers = %+v, want the acknowledgement recorded", k.CallbackAnswers())
	}
	want := CallbackAnswer{QueryID: "q-1", Text: "saved", ShowAlert: true, CacheTime: 30}
	if answer != want {
		t.Errorf("answer = %+v, want %+v", answer, want)
	}
	if len(k.CallbackAnswers()) != 1 {
		t.Errorf("answers = %+v, want exactly one", k.CallbackAnswers())
	}
}

func mustSend(t *testing.T, b *bot.Bot, text string) *models.Message {
	t.Helper()
	m, err := b.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: testChatID, Text: text})
	if err != nil {
		t.Fatalf("SendMessage %q: %v", text, err)
	}
	return m
}
