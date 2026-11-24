package hashio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileSharedVectors pins hashio.File to the same vectors the object store
// uses for its staging hashFile. The modules are separate, so the vectors are
// the parity proof for the two file-hashing entry points.
func TestFileSharedVectors(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.bin")
			if err := os.WriteFile(path, []byte(tc.data), 0o644); err != nil {
				t.Fatal(err)
			}
			got, size, err := File(path)
			if err != nil {
				t.Fatalf("File: %v", err)
			}
			if got != tc.want {
				t.Fatalf("File = %q, want %q", got, tc.want)
			}
			if size != int64(len(tc.data)) {
				t.Fatalf("File size = %d, want %d", size, len(tc.data))
			}
		})
	}
}

func TestReaderMatchesFile(t *testing.T) {
	fromFile, _, err := Reader(strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if fromFile != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("Reader = %q", fromFile)
	}
}
