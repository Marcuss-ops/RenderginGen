// Package store implements a disk-backed object store keyed by content hash.
package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("object not found")

// Store is a disk-backed object store.
type Store struct {
	dir string
}

// New creates a store rooted at dir.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Put writes object data for key. Small callers retain the byte API; the
// canonical large-object path is PutReader.
func (s *Store) Put(key string, data []byte) error {
	return s.PutReader(key, bytesReader(data))
}

// PutReader streams an object to a temporary file and atomically installs it.
// The complete request body is never held in memory by the object-store server.
func (s *Store) PutReader(key string, r io.Reader) error {
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".object-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, p)
}

// Open opens object data for streaming and returns its size.
func (s *Store) Open(key string) (*os.File, int64, error) {
	f, err := os.Open(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// Get reads object data for key. It is retained for small/test callers; HTTP
// delivery uses Open so large objects stream directly from disk.
func (s *Store) Get(key string) ([]byte, error) {
	data, err := os.ReadFile(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Store) path(key string) string {
	prefix := key
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(s.dir, "objects", prefix, key)
}

// byteReader avoids pulling bytes.NewReader into every small Store caller.
type byteReader []byte

func bytesReader(data []byte) *byteReader {
	r := byteReader(data)
	return &r
}

func (r *byteReader) Read(p []byte) (int, error) {
	if len(*r) == 0 {
		return 0, io.EOF
	}
	n := copy(p, *r)
	*r = (*r)[n:]
	return n, nil
}
