package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ErrNotFound is returned when an object is absent from L3.
var ErrNotFound = fmt.Errorf("object not found")

// Backend is the L3 central object storage.
type Backend interface {
	Fetch(ctx context.Context, key string) ([]byte, error)
	Store(ctx context.Context, key string, data []byte) error
}

// Memory is an in-memory Backend for tests and local development.
type Memory struct {
	mu      sync.Mutex
	objects map[string][]byte
}

// NewMemory creates an empty in-memory backend.
func NewMemory() *Memory {
	return &Memory{objects: make(map[string][]byte)}
}

func (m *Memory) Fetch(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

func (m *Memory) Store(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = append([]byte(nil), data...)
	return nil
}

// HTTP is a Backend backed by a simple object store over HTTP:
//
//	GET {base}/objects/{key} -> 200 body | 404
//	PUT {base}/objects/{key} -> 200/201/204
type HTTP struct {
	base   string
	client *http.Client
}

// NewHTTP creates an HTTP backend pointing at an object store base URL.
func NewHTTP(base string) *HTTP {
	return &HTTP{base: base, client: &http.Client{Timeout: 60 * time.Second}}
}

func (h *HTTP) Fetch(ctx context.Context, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+"/objects/"+key, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: unexpected status %d", key, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (h *HTTP) Store(ctx context.Context, key string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, h.base+"/objects/"+key, bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	default:
		return fmt.Errorf("store %s: unexpected status %d", key, resp.StatusCode)
	}
}
