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
	form := map[string]string{"chat_id": strconv.FormatInt(chatID, 10)}
	for i := 0; i+1 < len(fields); i += 2 {
		form[fields[i]] = fields[i+1]
	}
	if _, given := form[param]; !given {
		form[param] = k.Upload(param, "", nil).ID
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

// A relay's whole point is that the same file comes out the other side, so a
// received message has to name the one it carries: a test holds nothing else to
// reach the bytes by.
func TestAReceivedMessageNamesItsFile(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada, bob := k.User(7), k.User(8)

	ada.SendVoice("note.ogg", []byte("ogg"), "listen")
	sent := ada.History()[0]

	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: bob.ChatID(), FromChatID: ada.ChatID(), MessageID: sent.ID,
	}); err != nil {
		t.Fatalf("copy: %v", err)
	}

	got := bob.History()[0]
	if got.FileID == "" || got.FileID != sent.FileID {
		t.Errorf("bob received file %q, ada sent %q", got.FileID, sent.FileID)
	}
	if f, ok := k.File(got.FileID); !ok || string(f.Data) != "ogg" {
		t.Errorf("file = %+v, want the bytes ada sent", f)
	}

	ada.Send("plain")
	if named := ada.History()[1].FileID; named != "" {
		t.Errorf("a message carrying no file named %q", named)
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

func editMedia(t *testing.T, k *Kitchen, messageID int, media string) apiReply {
	t.Helper()
	return callForm(t, k, "editMessageMedia", map[string]string{
		"chat_id":    strconv.FormatInt(testChatID, 10),
		"message_id": strconv.Itoa(messageID), "media": media,
	})
}

func TestEditedMediaReplacesTheKindItCarried(t *testing.T) {
	k := New(t)
	sendMedia(t, k, testChatID, "sendPhoto", "photo", "caption", "before")

	sent := k.History(testChatID)[0]
	clip := k.Upload("video", "", nil).ID
	if reply := editMedia(t, k, sent.ID, `{"type":"video","media":"`+clip+`","caption":"after"}`); !reply.OK {
		t.Fatalf("edit = %+v, want it served", reply)
	}

	// One message still, now a video: the old kind has to go, not sit alongside.
	edited := k.History(testChatID)
	if len(edited) != 1 || edited[0].Media != "video" || edited[0].Text != "after" {
		t.Errorf("history = %v, want the one message carrying a video", edited)
	}
}

func TestOnlyTheKindsTelegramCanEditIn(t *testing.T) {
	k := New(t)
	sendMedia(t, k, testChatID, "sendPhoto", "photo")
	sent := k.History(testChatID)[0]

	for _, kind := range []string{"sticker", "voice", "video_note", "location", ""} {
		reply := editMedia(t, k, sent.ID, `{"type":"`+kind+`","media":"file-id"}`)
		if reply.OK || !strings.Contains(reply.Description, "not supported") {
			t.Errorf("editing in a %s = %+v, want it refused", kind, reply)
		}
	}
}

// A location reads like media in a transcript without being any, and a copy is
// how one ends up in a message the bot owns and could otherwise edit.
func TestThereHasToBeMediaToEdit(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})

	if _, err := b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: testChatID, Text: "words",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	here := k.User(testChatID)
	here.ShareLocation(51.5, -0.12)
	if _, err := b.CopyMessage(context.Background(), &bot.CopyMessageParams{
		ChatID: testChatID, FromChatID: testChatID, MessageID: here.Screen().ID,
	}); err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}

	photo := k.Upload("photo", "", nil).ID
	for _, m := range k.History(testChatID) {
		if m.From != "Kitchen" {
			continue
		}
		reply := editMedia(t, k, m.ID, `{"type":"photo","media":"`+photo+`"}`)
		if reply.OK || !strings.Contains(reply.Description, "no media in the message") {
			t.Errorf("editing %q = %+v, want it refused", m, reply)
		}
	}
}

func TestEditingMediaToWhatIsAlreadyThereChangesNothing(t *testing.T) {
	k := New(t)
	photo := k.Upload("photo", "", nil).ID
	sendMedia(t, k, testChatID, "sendPhoto", "photo", "photo", photo)
	sent := k.History(testChatID)[0]

	reply := editMedia(t, k, sent.ID, `{"type":"photo","media":"`+photo+`"}`)
	if reply.OK || !strings.Contains(reply.Description, "not modified") {
		t.Errorf("edit = %+v, want the same media refused", reply)
	}
}

// A file uploaded with the edit arrives under a field of its own, which the
// media parameter points at instead of carrying.
func TestEditedMediaTakesAnUploadedFile(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	sendMedia(t, k, testChatID, "sendPhoto", "photo")
	sent := k.History(testChatID)[0]

	if _, err := b.EditMessageMedia(context.Background(), &bot.EditMessageMediaParams{
		ChatID:    testChatID,
		MessageID: sent.ID,
		Media: &models.InputMediaVideo{
			Media:           "attach://clip.mp4",
			MediaAttachment: strings.NewReader("mp4-bytes"),
			Caption:         "the clip",
		},
	}); err != nil {
		t.Fatalf("EditMessageMedia: %v", err)
	}

	edited := k.History(testChatID)
	if len(edited) != 1 || edited[0].Media != "video" || edited[0].Text != "the clip" {
		t.Fatalf("history = %v, want the uploaded video in its place", edited)
	}

	// The bytes have to be reachable through the id the message ended up with,
	// or the edit kept the pointer instead of following it.
	stored, _ := k.world.latest(testChatID)
	if f, held := k.files.get(fileIn(&stored)); !held || string(f.Data) != "mp4-bytes" {
		t.Errorf("file %q = %+v %v, want the bytes that came with the edit", fileIn(&stored), f, held)
	}
}

func TestAFileIDHasToBeOneTheBotWasGiven(t *testing.T) {
	k := New(t)
	for _, kind := range mediaKinds {
		reply := sendMedia(t, k, testChatID, kind.method, kind.param, kind.param, "never-issued")
		if reply.OK || !strings.Contains(reply.Description, "wrong file identifier") {
			t.Errorf("%s = %+v, want an id nobody issued refused", kind.method, reply)
		}
	}
	photo := k.Upload("photo", "", nil).ID
	reply := sendAlbum(t, k, `[{"type":"photo","media":"`+photo+`"},{"type":"photo","media":"never-issued"}]`)
	if reply.OK || !strings.Contains(reply.Description, "wrong file identifier") {
		t.Errorf("album = %+v, want one unknown id to refuse all of it", reply)
	}
	if sent := k.History(testChatID); len(sent) != 0 {
		t.Fatalf("history = %v, want nothing to have landed", sent)
	}

	sendMedia(t, k, testChatID, "sendPhoto", "photo", "photo", photo)
	sent := k.History(testChatID)[0]
	if reply := editMedia(t, k, sent.ID, `{"type":"photo","media":"never-issued"}`); reply.OK {
		t.Errorf("edit = %+v, want an id nobody issued refused", reply)
	}
	if kept := k.History(testChatID)[0].FileID; kept != photo {
		t.Errorf("file = %q, want the photo left as it was (%q)", kept, photo)
	}
}

func TestAFileIsSentAgainOnlyAsWhatItIs(t *testing.T) {
	k := New(t)
	photo := k.Upload("photo", "face.jpg", []byte("jpeg")).ID
	for _, kind := range mediaKinds {
		if kind.param == "photo" {
			continue
		}
		reply := sendMedia(t, k, testChatID, kind.method, kind.param, kind.param, photo)
		if reply.OK || !strings.Contains(reply.Description, "type of file mismatch") {
			t.Errorf("%s with a photo's id = %+v, want it refused", kind.method, reply)
		}
	}
	video := k.Upload("video", "", nil).ID
	if reply := sendAlbum(t, k, `[{"type":"photo","media":"`+photo+`"},{"type":"photo","media":"`+video+`"}]`); reply.OK {
		t.Errorf("album = %+v, want a video sent as a photo refused", reply)
	}
	if sent := k.History(testChatID); len(sent) != 0 {
		t.Fatalf("history = %v, want nothing to have landed", sent)
	}

	if reply := sendMedia(t, k, testChatID, "sendPhoto", "photo", "photo", photo); !reply.OK {
		t.Fatalf("sendPhoto = %+v, want a photo sent as itself served", reply)
	}
	sent := k.History(testChatID)[0]
	reply := editMedia(t, k, sent.ID, `{"type":"document","media":"`+photo+`"}`)
	if reply.OK || !strings.Contains(reply.Description, "type of file mismatch") {
		t.Errorf("edit = %+v, want a photo refused as a document", reply)
	}
}

// A URL is not an id: Telegram fetches what it points at, and that is a file of its own from then on.
func TestAFileSentByURLBecomesOneTheBotHolds(t *testing.T) {
	k := New(t)
	const address = "https://example.com/face.jpg"
	if reply := sendMedia(t, k, testChatID, "sendPhoto", "photo", "photo", address); !reply.OK {
		t.Fatalf("sendPhoto by URL = %+v, want it served", reply)
	}

	fetched := k.History(testChatID)[0].FileID
	if f, ok := k.File(fetched); !ok || f.Name != address {
		t.Errorf("file %q = %+v %v, want one named by its address", fetched, f, ok)
	}
	if reply := sendMedia(t, k, testChatID, "sendPhoto", "photo", "photo", fetched); !reply.OK {
		t.Errorf("re-send = %+v, want the fetched photo sent again", reply)
	}
	if reply := sendMedia(t, k, testChatID, "sendDocument", "document", "document", fetched); reply.OK {
		t.Errorf("as a document = %+v, want it refused", reply)
	}
}

func TestAnUploadIsWhatItWasFirstSentAs(t *testing.T) {
	k := New(t)
	b := newClient(t, k)
	ctx := context.Background()

	sent, err := b.SendVoice(ctx, &bot.SendVoiceParams{
		ChatID: testChatID, Voice: &models.InputFileUpload{Filename: "note.ogg", Data: strings.NewReader("ogg")},
	})
	if err != nil {
		t.Fatalf("SendVoice: %v", err)
	}
	if _, err := b.SendVoice(ctx, &bot.SendVoiceParams{
		ChatID: otherChatID, Voice: &models.InputFileString{Data: sent.Voice.FileID},
	}); err != nil {
		t.Errorf("re-send as a voice note: %v", err)
	}
	_, err = b.SendAudio(ctx, &bot.SendAudioParams{
		ChatID: otherChatID, Audio: &models.InputFileString{Data: sent.Voice.FileID},
	})
	if err == nil || !strings.Contains(err.Error(), "type of file mismatch") {
		t.Errorf("err = %v, want an uploaded voice note refused as audio", err)
	}
}
