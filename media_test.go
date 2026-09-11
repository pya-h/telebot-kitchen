package kitchen

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestMediaStoreIssuesStableIDs(t *testing.T) {
	k := New(t)

	first := k.files.add("a.jpg", []byte("first"))
	second := k.files.add("b.jpg", []byte("second"))
	if first.ID == second.ID {
		t.Fatalf("both uploads got file id %q", first.ID)
	}

	stored, ok := k.File(first.ID)
	if !ok || string(stored.Data) != "first" || stored.Name != "a.jpg" {
		t.Errorf("stored file = %+v, %v; want the first upload", stored, ok)
	}
	if _, ok := k.File("file-unknown"); ok {
		t.Error("unknown file id resolved to a file")
	}
}

func TestUploadReadsBackAsFileID(t *testing.T) {
	k := New(t)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("chat_id", "101"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	part, err := form.CreateFormFile("photo", "shot.jpg")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write([]byte("bytes")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, k.methodURL("sendPhoto"), &body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())

	p, err := k.parseParams(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p["chat_id"] != "101" {
		t.Errorf("chat_id = %q, want the plain field", p["chat_id"])
	}

	stored, ok := k.File(p["photo"])
	if !ok || string(stored.Data) != "bytes" || stored.Name != "shot.jpg" {
		t.Errorf("photo param %q did not resolve to the upload: %+v, %v", p["photo"], stored, ok)
	}
}

// Telegram resends a photo with all of its sizes, whichever size's id it was given.
func TestEachPhotoSizeIsAnIDForTheWholePhoto(t *testing.T) {
	k := talking(t)
	b := newClient(t, k)
	k.DeliverTo(func(context.Context, *models.Update) {})
	k.User(7).SendPhoto("face.jpg", []byte("jpeg"), "")
	received, _ := k.world.latest(7)
	sizes := received.Photo

	ids, uniques := map[string]bool{}, map[string]bool{}
	for _, size := range sizes {
		ids[size.FileID], uniques[size.FileUniqueID] = true, true
		if f, ok := k.File(size.FileID); !ok || string(f.Data) != "jpeg" {
			t.Errorf("the %dpx size = %+v %v, want it to read back as the photo", size.Width, f, ok)
		}
	}
	if len(ids) != len(sizes) || len(uniques) != len(sizes) {
		t.Errorf("sizes = %+v, want an id and a unique id of their own for each", sizes)
	}

	for _, size := range []models.PhotoSize{sizes[0], sizes[len(sizes)-1]} {
		resent, err := b.SendPhoto(context.Background(), &bot.SendPhotoParams{
			ChatID: otherChatID, Photo: &models.InputFileString{Data: size.FileID},
		})
		if err != nil {
			t.Fatalf("re-send the %dpx size: %v", size.Width, err)
		}
		if !slices.Equal(resent.Photo, sizes) {
			t.Errorf("re-sending the %dpx size landed %+v, want every size of the photo", size.Width, resent.Photo)
		}
	}
}

func TestUploadTakesOnlyAKindOfFile(t *testing.T) {
	tb := &recordingTB{}
	defer tb.close()

	New(tb).Upload("gif", "loop.gif", nil)
	if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "animation") {
		t.Errorf("errors = %v, want one naming the kinds there are", errs)
	}
}
