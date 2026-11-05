package main

import (
	"io"
	"testing"
)

func TestRunRequiresPlace(t *testing.T) {
	if code := run(nil, io.Discard, io.Discard); code != 2 {
		t.Fatalf("run without -place = %d, want 2", code)
	}
}
