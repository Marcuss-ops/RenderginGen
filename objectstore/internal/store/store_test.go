package store

import (
	"errors"
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

func TestGetMissing(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
