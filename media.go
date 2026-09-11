package kitchen

import (
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"sync"

	"github.com/go-telegram/bot/models"
)

// Telegram sends a ladder of thumbnails, largest last. The kitchen never decodes
// the bytes, so each rung is only an id of its own for the one file it holds.
var photoLadder = []int{90, 320, 800}

type File struct {
	ID       string
	UniqueID string
	Name     string
	Data     []byte
}

type storedFile struct {
	File
	kind string // "" for an upload no message has carried yet
}

type mediaStore struct {
	mu     sync.RWMutex
	nextID int64
	files  map[string]*storedFile // under every id Telegram would take for it
}

func newMediaStore() *mediaStore { return &mediaStore{files: map[string]*storedFile{}} }

// add holds bytes that came with a call, before any message has said what they are.
func (s *mediaStore) add(name string, data []byte) File {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hold(name, data).File
}

func (s *mediaStore) issue(kind, name string, data []byte) File {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.hold(name, data)
	s.settle(f, kind)
	return f.File
}

func (s *mediaStore) hold(name string, data []byte) *storedFile {
	s.nextID++
	f := &storedFile{File: File{
		ID:       fmt.Sprintf("file-%d", s.nextID),
		UniqueID: fmt.Sprintf("unique-%d", s.nextID),
		Name:     name,
		Data:     data,
	}}
	s.files[f.ID] = f
	return f
}

func (s *mediaStore) settle(f *storedFile, kind string) {
	f.kind = kind
	if kind == "photo" {
		for rung := range photoLadder {
			s.files[sized(f.ID, rung)] = f
		}
	}
}

func (s *mediaStore) upload(header *multipart.FileHeader) (File, error) {
	part, err := header.Open()
	if err != nil {
		return File{}, err
	}
	defer part.Close()

	data, err := io.ReadAll(part)
	if err != nil {
		return File{}, err
	}
	return s.add(header.Filename, data), nil
}

func (s *mediaStore) get(id string) (File, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.files[id]
	if !ok {
		return File{}, false
	}
	return f.File, true
}

// resolve is the file a send names. A URL is fetched, so it is a new file; an id
// has to be one this bot was given.
func (s *mediaStore) resolve(ref, kind string) (File, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return s.issue(kind, ref, nil), nil
	}
	return s.named(ref, kind)
}

func (s *mediaStore) named(id, kind string) (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, ok := s.files[id]
	switch {
	case !ok:
		return File{}, requestError("wrong file identifier/HTTP URL specified")
	case f.kind == "":
		s.settle(f, kind)
	case f.kind != kind:
		return File{}, requestError("type of file mismatch")
	}
	return f.File, nil
}

// recall holds a recorded message's file under the ids Telegram gave it, so the bot
// may send it again by any of them. Thumbnails stay out, as Telegram resends none.
func (s *mediaStore) recall(m *models.Message) {
	kind, id, uniqueID := fileOn(m)
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, known := s.files[id]
	if !known {
		f = &storedFile{File: File{ID: id, UniqueID: uniqueID}}
		s.files[id] = f
		s.settle(f, kind)
	}
	for _, size := range m.Photo {
		if _, taken := s.files[size.FileID]; !taken {
			s.files[size.FileID] = f
		}
	}
}

func photoSizes(f File) []models.PhotoSize {
	sizes := make([]models.PhotoSize, len(photoLadder))
	for rung, dimension := range photoLadder {
		sizes[rung] = models.PhotoSize{
			FileID:       sized(f.ID, rung),
			FileUniqueID: sized(f.UniqueID, rung),
			Width:        dimension,
			Height:       dimension,
			FileSize:     len(f.Data),
		}
	}
	return sizes
}

// The largest rung goes by the file's own id, the one a message names its file by.
func sized(id string, rung int) string {
	if rung == len(photoLadder)-1 {
		return id
	}
	return fmt.Sprintf("%s-%d", id, photoLadder[rung])
}
