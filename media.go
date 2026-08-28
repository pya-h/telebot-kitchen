package kitchen

import (
	"fmt"
	"io"
	"mime/multipart"
	"sync"
	"sync/atomic"
)

type File struct {
	ID       string
	UniqueID string
	Name     string
	Data     []byte
}

type mediaStore struct {
	mu     sync.RWMutex
	nextID atomic.Int64
	files  map[string]File
}

func newMediaStore() *mediaStore { return &mediaStore{files: map[string]File{}} }

func (s *mediaStore) add(name string, data []byte) File {
	n := s.nextID.Add(1)
	f := File{
		ID:       fmt.Sprintf("file-%d", n),
		UniqueID: fmt.Sprintf("unique-%d", n),
		Name:     name,
		Data:     data,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
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
