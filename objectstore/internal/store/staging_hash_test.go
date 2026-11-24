package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHashFileSharedVectors pins the object store's one file-hashing primitive
// to the same vectors the RenderingGen module uses for hashio.File. The two
// modules cannot share code without coupling their release cadence, so the
// vectors are the cross-module parity proof.
func TestHashFileSharedVectors(t *testing.T) {
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
			path := filepath.Join(t.TempDir(), "object.bin")
			if err := os.WriteFile(path, []byte(tc.data), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := hashFile(path)
			if err != nil {
				t.Fatalf("hashFile: %v", err)
			}
			if got != tc.want {
				t.Fatalf("hashFile = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHashFilePropagatesOpenError(t *testing.T) {
	if _, err := hashFile(filepath.Join(t.TempDir(), "missing.bin")); err == nil {
		t.Fatal("hashFile must report a missing file")
	}
}
