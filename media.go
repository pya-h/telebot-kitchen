package kitchen

import (
	"fmt"
	"io"
	"mime/multipart"
	"sync"

	"github.com/go-telegram/bot/models"
)

// Telegram sends a ladder of thumbnails, largest last, and bot code picks the
// one it wants by position. The kitchen never decodes the bytes, so the entries
// are square placeholders that all address the one file the store holds.
var photoLadder = []int{90, 320, 800}

type File struct {
	ID       string
	UniqueID string
	Name     string
	Data     []byte
}

type mediaStore struct {
	mu     sync.RWMutex
	nextID int64
	files  map[string]File
}

func newMediaStore() *mediaStore { return &mediaStore{files: map[string]File{}} }

func (s *mediaStore) add(name string, data []byte) File {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	f := File{
		ID:       fmt.Sprintf("file-%d", s.nextID),
		UniqueID: fmt.Sprintf("unique-%d", s.nextID),
		Name:     name,
		Data:     data,
	}
	s.files[f.ID] = f
	return f
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
	return f, ok
}

// A file id the store never issued is one the bot re-sent from an earlier
// message, so it stays addressable even though the bytes are unknown here.
func (s *mediaStore) photoSizes(fileID string) []models.PhotoSize {
	f, ok := s.get(fileID)
	if !ok {
		f = File{ID: fileID, UniqueID: "unique-" + fileID}
	}
	sizes := make([]models.PhotoSize, len(photoLadder))
	for i, dimension := range photoLadder {
		sizes[i] = models.PhotoSize{
			FileID:       f.ID,
			FileUniqueID: f.UniqueID,
			Width:        dimension,
			Height:       dimension,
			FileSize:     len(f.Data),
		}
	}
	return sizes
}
