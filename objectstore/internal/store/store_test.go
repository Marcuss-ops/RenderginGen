package store

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestPutGet(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Put("abc", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := s.Get("abc")
	if err != nil || string(data) != "hello" {
		t.Fatalf("get: %v %q", err, data)
	}
}

func TestPutReaderStreamsAndInstalls(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	payload := bytes.Repeat([]byte("x"), 64*1024)
	if err := s.PutReader("streamed", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("streamed")
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("streamed get: %v, %d bytes", err, len(got))
	}
	if _, err := os.Stat(s.path("streamed")); err != nil {
		t.Fatalf("object path missing: %v", err)
	}
}

func TestGetMissing(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
