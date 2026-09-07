package kitchen

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func sendAlbum(t *testing.T, k *Kitchen, media string) apiReply {
	t.Helper()
	return callForm(t, k, "sendMediaGroup", map[string]string{
		"chat_id": strconv.FormatInt(testChatID, 10), "media": media,
	})
}

func TestAnAlbumIsSeveralMessagesUnderOneGroup(t *testing.T) {
	k := New(t)
	reply := sendAlbum(t, k, `[
		{"type":"photo","media":"one","caption":"the pair"},
		{"type":"video","media":"two"}
	]`)
	if !reply.OK {
		t.Fatalf("sendMediaGroup = %+v, want it served", reply)
	}

	sent := k.History(testChatID)
	if len(sent) != 2 || sent[0].Media != "photo" || sent[1].Media != "video" {
		t.Fatalf("history = %v, want a photo and a video", sent)
	}
	if sent[0].Album == "" || sent[0].Album != sent[1].Album {
		t.Errorf("groups = %q and %q, want one shared between them", sent[0].Album, sent[1].Album)
	}
	if sent[0].Text != "the pair" || sent[1].Text != "" {
		t.Errorf("captions = %q and %q, want each item to keep its own", sent[0].Text, sent[1].Text)
	}
}

// Two albums in a chat are two groups, or a bot reading by group would read both as one.
func TestTwoAlbumsAreTwoGroups(t *testing.T) {
	k := New(t)
	pair := `[{"type":"photo","media":"one"},{"type":"photo","media":"two"}]`
	sendAlbum(t, k, pair)
	sendAlbum(t, k, pair)

	sent := k.History(testChatID)
	if len(sent) != 4 || sent[1].Album == sent[2].Album {
		t.Errorf("groups = %v, want the second album under its own", sent)
	}
}

func TestAnAlbumIsBetweenTwoAndTenFiles(t *testing.T) {
	k := New(t)
	one := `{"type":"photo","media":"x"}`
	for _, group := range []string{"[]", "[" + one + "]", "[" + strings.Repeat(one+",", 10) + one + "]"} {
		reply := sendAlbum(t, k, group)
		if reply.OK || !strings.Contains(reply.Description, "between 2 and 10") {
			t.Errorf("reply = %+v, want the count refused", reply)
		}
	}
	if sent := k.History(testChatID); len(sent) != 0 {
		t.Errorf("history = %v, want nothing to have landed", sent)
	}
}

func TestOnlySomeKindsTravelTogether(t *testing.T) {
	k := New(t)
	for group, want := range map[string]string{
		`[{"type":"document","media":"a"},{"type":"photo","media":"b"}]`:  "document must be the only kind",
		`[{"type":"audio","media":"a"},{"type":"video","media":"b"}]`:     "audio must be the only kind",
		`[{"type":"sticker","media":"a"},{"type":"sticker","media":"b"}]`: "cannot travel in a media group",
	} {
		reply := sendAlbum(t, k, group)
		if reply.OK || !strings.Contains(reply.Description, want) {
			t.Errorf("reply = %+v, want %q", reply, want)
		}
	}

	if reply := sendAlbum(t, k, `[
		{"type":"document","media":"a"},{"type":"document","media":"b"}
	]`); !reply.OK {
		t.Errorf("an album of documents = %+v, want it served", reply)
	}
}

func TestAnAlbumTheChatRefusesIsNotSent(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	team := k.Group(-42, "Standup")
	k.User(7).In(team).RemoveBot()

	reply := callForm(t, k, "sendMediaGroup", map[string]string{
		"chat_id": "-42",
		"media":   `[{"type":"photo","media":"a"},{"type":"photo","media":"b"}]`,
	})
	if reply.OK || !strings.Contains(reply.Description, "was kicked") {
		t.Errorf("reply = %+v, want it refused like any other send", reply)
	}
}

// A member's album has to be whole before the bot sees any of it: a reply to the
// first photo must not be stepped over by the id of the last.
func TestAMemberSendsAnAlbumWhole(t *testing.T) {
	k := New(t)
	var seen int
	b := syncBot(t, k, func(ctx context.Context, c *bot.Bot, u *models.Update) {
		if u.Message == nil || u.Message.MediaGroupID == "" {
			return
		}
		seen++
		if seen == 1 {
			c.SendMessage(ctx, &bot.SendMessageParams{ChatID: u.Message.Chat.ID, Text: "nice pictures"})
		}
	})
	k.DeliverTo(b.ProcessUpdate)
	ada := k.User(7)

	ada.SendAlbum(
		Photo("one.jpg", []byte("a"), "us"),
		Photo("two.jpg", []byte("b"), ""),
		Video("three.mp4", []byte("c"), ""),
	)

	if seen != 3 {
		t.Errorf("the bot saw %d of the album, want all three", seen)
	}
	ada.Expect(TextIs("nice pictures"))
	ada.ExpectNothingMore()

	sent := k.History(ada.ChatID())
	if len(sent) != 4 || sent[0].Album == "" || sent[0].Album != sent[2].Album {
		t.Errorf("history = %v, want three under one group", sent)
	}
}

func TestFilesAMemberSendsInAnAlbumAreKept(t *testing.T) {
	k := New(t)
	k.DeliverTo(func(context.Context, *models.Update) {})
	ada := k.User(7)

	ada.SendAlbum(Document("notes.pdf", []byte("pdf"), "read this"), Document("more.pdf", []byte("also"), ""))

	sent := k.History(ada.ChatID())
	if len(sent) != 2 || sent[0].Media != "document" || sent[0].Text != "read this" {
		t.Fatalf("history = %v, want two documents, the first captioned", sent)
	}
	stored, _ := k.world.latest(ada.ChatID())
	if f, held := k.files.get(fileIn(&stored)); !held || string(f.Data) != "also" {
		t.Errorf("file = %+v %v, want the bytes that were sent", f, held)
	}
}

func TestAMemberIsToldWhenAnAlbumCannotTravel(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	k := New(tb)
	k.DeliverTo(func(context.Context, *models.Update) {})
	k.User(7).SendAlbum(Photo("one.jpg", []byte("a"), ""))

	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "between 2 and 10") {
		t.Errorf("errors = %v, want one about the count", errs)
	}
}

// The library builds the group the way a real bot does, uploads and all.
func TestAnAlbumBuiltByTheLibraryArrives(t *testing.T) {
	k := New(t)
	b := newClient(t, k)

	sent, err := b.SendMediaGroup(context.Background(), &bot.SendMediaGroupParams{
		ChatID: testChatID,
		Media: []models.InputMedia{
			&models.InputMediaPhoto{Media: "attach://one.jpg", MediaAttachment: strings.NewReader("first"), Caption: "us"},
			&models.InputMediaPhoto{Media: "attach://two.jpg", MediaAttachment: strings.NewReader("second")},
		},
	})
	if err != nil {
		t.Fatalf("SendMediaGroup: %v", err)
	}
	if len(sent) != 2 || sent[0].MediaGroupID == "" || sent[0].MediaGroupID != sent[1].MediaGroupID {
		t.Fatalf("sent = %+v, want two messages under one group", sent)
	}
	if f, held := k.files.get(fileIn(sent[1])); !held || string(f.Data) != "second" {
		t.Errorf("file = %+v %v, want the bytes uploaded with it", f, held)
	}
}
