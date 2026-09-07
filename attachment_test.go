package kitchen

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// send drives one media method the way a bot does, by its own name.
func sendMedia(t *testing.T, k *Kitchen, chatID int64, method, param string, fields ...string) apiReply {
	t.Helper()
	form := map[string]string{"chat_id": strconv.FormatInt(chatID, 10), param: "file-id"}
	for i := 0; i+1 < len(fields); i += 2 {
		form[fields[i]] = fields[i+1]
	}
	return callForm(t, k, method, form)
}

// Every kind the table registers must be one mediaOf recognises, or a message
// the bot sent would render as if it carried nothing.
func TestEverySendKindArrivesAsItself(t *testing.T) {
	for _, kind := range mediaKinds {
		t.Run(kind.method, func(t *testing.T) {
			k := New(t)
			if reply := sendMedia(t, k, testChatID, kind.method, kind.param); !reply.OK {
				t.Fatalf("%s = %+v, want it served", kind.method, reply)
			}

			sent := k.History(testChatID)
			if len(sent) != 1 || sent[0].Media == "" {
				t.Fatalf("history = %v, want one message saying what it carries", sent)
			}
			if want := "**Kitchen:** (" + sent[0].Media + ")\n"; k.Transcript(testChatID) != want {
				t.Errorf("transcript = %q, want %q", k.Transcript(testChatID), want)
			}
		})
	}
}

func TestASendIsRefusedWithoutItsFile(t *testing.T) {
	k := New(t)
	for _, kind := range mediaKinds {
		reply := callForm(t, k, kind.method, map[string]string{"chat_id": strconv.FormatInt(testChatID, 10)})
		if reply.OK || !strings.Contains(reply.Description, kind.param) {
			t.Errorf("%s = %+v, want a refusal naming %q", kind.method, reply, kind.param)
		}
	}
}

// Telegram allows no caption on these two, so one sent anyway is dropped.
func TestAStickerAndAVideoNoteCarryNoCaption(t *testing.T) {
	k := New(t)
	sendMedia(t, k, testChatID, "sendSticker", "sticker", "caption", "say something")
	sendMedia(t, k, testChatID, "sendVideoNote", "video_note", "caption", "say something")

	for _, m := range k.History(testChatID) {
		if m.Text != "" {
			t.Errorf("%s carries %q, want no caption", m.Media, m.Text)
		}
	}
}

func TestACaptionIsEditableOnlyWhereTelegramAllowsOne(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	sendMedia(t, k, testChatID, "sendVoice", "voice", "caption", "first")
	sendMedia(t, k, testChatID, "sendSticker", "sticker")
	voice, sticker := k.History(testChatID)[0], k.History(testChatID)[1]

	if _, err := b.EditMessageCaption(context.Background(), &bot.EditMessageCaptionParams{
		ChatID: testChatID, MessageID: voice.ID, Caption: "second",
	}); err != nil {
		t.Fatalf("EditMessageCaption on a voice note: %v", err)
	}
	if got := k.History(testChatID)[0].Text; got != "second" {
		t.Errorf("caption = %q, want the reworded one", got)
	}

	_, err := b.EditMessageCaption(context.Background(), &bot.EditMessageCaptionParams{
		ChatID: testChatID, MessageID: sticker.ID, Caption: "second",
	})
	if err == nil || !strings.Contains(err.Error(), "no caption") {
		t.Errorf("err = %v, want a sticker to refuse a caption", err)
	}
}

func TestTextIsNotEditableOnAnythingCarryingMedia(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	sendMedia(t, k, testChatID, "sendVideo", "video")
	_, err := b.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID: testChatID, MessageID: k.History(testChatID)[0].ID, Text: "words",
	})
	if err == nil || !strings.Contains(err.Error(), "no text") {
		t.Errorf("err = %v, want a video to refuse text", err)
	}
}

// The relay the kitchen exists for: a voice note reaching the other side with
// nothing of the sender left on it.
func TestAVoiceNoteRelaysWithoutItsSender(t *testing.T) {
	k := New(t)
	b := syncBot(t, k, func(ctx context.Context, b *bot.Bot, u *models.Update) {
		b.CopyMessage(ctx, &bot.CopyMessageParams{
			ChatID: otherChatID, FromChatID: u.Message.Chat.ID, MessageID: u.Message.ID,
		})
	})
	k.DeliverTo(b.ProcessUpdate)

	k.User(testChatID, WithFullName("Ada", "Lovelace")).SendVoice("note.ogg", []byte("ogg"), "hi")

	landed := k.History(otherChatID)
	if len(landed) != 1 || landed[0].Media != "voice" || landed[0].Text != "hi" {
		t.Fatalf("chat = %v, want the voice note and its caption", landed)
	}
	if !landed[0].FromBot || strings.Contains(landed[0].From, "Ada") {
		t.Errorf("from = %q, want the bot rather than whoever spoke", landed[0].From)
	}
}

func TestAMemberSendsEveryKindOfMedia(t *testing.T) {
	k := New(t)
	var got []Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { got = append(got, k.view(*u.Message)) })

	ada := k.User(7)
	ada.SendVoice("note.ogg", []byte("ogg"), "listen")
	ada.SendAudio("song.mp3", []byte("mp3"), "")
	ada.SendVideo("clip.mp4", []byte("mp4"), "")
	ada.SendAnimation("loop.gif", []byte("gif"), "")
	ada.SendDocument("cv.pdf", []byte("pdf"), "")
	ada.SendSticker("wave.webp", []byte("webp"))
	ada.SendVideoNote("round.mp4", []byte("mp4"))

	want := []string{"voice", "audio", "video", "animation", "document", "sticker", "video note"}
	for i, kind := range want {
		if i >= len(got) || got[i].Media != kind {
			t.Fatalf("updates = %v, want %v", got, want)
		}
	}
	if got[0].Text != "listen" {
		t.Errorf("caption = %q, want the one it was sent with", got[0].Text)
	}
}

func TestMediaAMemberSendsIsReadableBack(t *testing.T) {
	k := New(t)
	var sent *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { sent = u.Message })

	k.User(7).SendDocument("cv.pdf", []byte("pdf-bytes"), "")

	f, ok := k.File(sent.Document.FileID)
	if !ok || f.Name != "cv.pdf" || string(f.Data) != "pdf-bytes" {
		t.Errorf("file = %+v %v, want the upload it stands for", f, ok)
	}
}

// A bot re-sending a file id it was given never uploaded anything, so the id
// has to stay addressable on the way back out.
func TestAResentFileIDStaysTheSameFile(t *testing.T) {
	k := New(t)
	var sent *models.Message
	k.DeliverTo(func(_ context.Context, u *models.Update) { sent = u.Message })
	k.User(7).SendVoice("note.ogg", []byte("ogg"), "")

	callForm(t, k, "sendVoice", map[string]string{
		"chat_id": strconv.FormatInt(otherChatID, 10), "voice": sent.Voice.FileID,
	})

	relayed, ok := k.world.latest(otherChatID)
	if !ok || relayed.Voice == nil || relayed.Voice.FileID != sent.Voice.FileID {
		t.Fatalf("relayed = %+v, want the id the bot was handed", relayed)
	}
	if relayed.Voice.FileSize != int64(len("ogg")) {
		t.Errorf("size = %d, want the file it stands for rather than a stub", relayed.Voice.FileSize)
	}
	if f, ok := k.File(relayed.Voice.FileID); !ok || string(f.Data) != "ogg" {
		t.Errorf("file = %+v, want the bytes still behind it", f)
	}
}

// A copy may reword what it carries, but only where the kind takes a caption.
func TestACopyMayReplaceTheCaptionOnMedia(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	sendMedia(t, k, testChatID, "sendVoice", "voice", "caption", "first")
	sendMedia(t, k, testChatID, "sendSticker", "sticker")
	voice, sticker := k.History(testChatID)[0], k.History(testChatID)[1]

	for _, id := range []int{voice.ID, sticker.ID} {
		if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
			ChatID: otherChatID, FromChatID: testChatID, MessageID: id, Caption: "second",
		}); err != nil {
			t.Fatalf("CopyMessage: %v", err)
		}
	}

	copies := k.History(otherChatID)
	if len(copies) != 2 || copies[0].Text != "second" {
		t.Errorf("copies = %v, want the voice note reworded", copies)
	}
	if copies[1].Text != "" {
		t.Errorf("sticker = %q, want a caption it cannot carry left off", copies[1].Text)
	}
}

func TestMediaTheChatRefusesIsNotSent(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-100, "Standup")
	k.User(7).In(team).RemoveBot()

	reply := sendMedia(t, k, team.ID(), "sendVoice", "voice")
	if reply.OK || !strings.Contains(reply.Description, "kicked") {
		t.Errorf("reply = %+v, want a chat the bot is out of to refuse it", reply)
	}
	if sent := k.History(team.ID()); len(sent) != 0 {
		t.Errorf("history = %v, want nothing to have landed", sent)
	}
}
