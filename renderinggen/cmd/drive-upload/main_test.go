package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
)

// TestUploadAndVerifyReportsTheRealDigest pins the fix for the hardcoded
// "verified-by-storage-key" sha256: the value the CLI prints is the SHA-256 of
// the bytes on disk, and an expected-address pin is enforced.
func TestUploadAndVerifyReportsTheRealDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.mp4")
	body := []byte("content-addressed bytes")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(body)
	wantHex := hex.EncodeToString(want[:])

	publisher := drive.NewMock("", 0)
	got, err := uploadAndVerify(context.Background(), publisher,
		drive.PublishRequest{Name: "out.mp4", Path: path}, wantHex)
	if err != nil {
		t.Fatalf("uploadAndVerify: %v", err)
	}
	if got.SHA256 != wantHex {
		t.Fatalf("reported sha256 = %q, want %q", got.SHA256, wantHex)
	}
	if got.Result.SizeBytes != int64(len(body)) {
		t.Fatalf("size = %d, want %d", got.Result.SizeBytes, len(body))
	}
}

func TestUploadAndVerifyRejectsWrongExpectedSHA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := uploadAndVerify(context.Background(), drive.NewMock("", 0),
		drive.PublishRequest{Name: "out.mp4", Path: path}, strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("want sha256 mismatch, got %v", err)
	}
}

// shortPublisher reports fewer bytes than the file holds, simulating a
// truncated/partial publication that must never be reported as a pass.
type shortPublisher struct{}

func (shortPublisher) Publish(context.Context, drive.PublishRequest) (drive.Result, error) {
	return drive.Result{FileID: "f", SizeBytes: 1}, nil
}

func TestUploadAndVerifyRejectsPartialUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(path, []byte("much longer than one byte"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := uploadAndVerify(context.Background(), shortPublisher{},
		drive.PublishRequest{Name: "out.mp4", Path: path}, "")
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("want size mismatch, got %v", err)
	}
}
