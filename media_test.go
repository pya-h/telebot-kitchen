package kitchen

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"
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
