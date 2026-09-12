package drive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// TestGooglePublishUsesConfiguredParentFolder guards the fix where Publish
// ignored the configured parent folder and uploaded to "My Drive" root.
func TestGooglePublishUsesConfiguredParentFolder(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-1","webViewLink":"https://drive.google.com/file/d/file-1/view"}`))
	}))
	defer srv.Close()

	svc, err := gdrive.NewService(context.Background(),
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	g := &Google{service: svc, parentFolder: "folder-123"}
	path := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := g.Publish(context.Background(), PublishRequest{
		Name:        "x.mp4",
		ContentType: "video/mp4",
		Path:        path,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.FileID != "file-1" || res.WebViewLink != "https://drive.google.com/file/d/file-1/view" {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(string(body), `"parents":["folder-123"]`) {
		t.Fatalf("upload did not use the configured parent folder; body=%s", body)
	}
}

// TestGooglePublishRequestParentOverridesConfigured ensures a per-request
// parent wins, and the configured folder never leaks into the request.
func TestGooglePublishRequestParentOverridesConfigured(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-2","webViewLink":""}`))
	}))
	defer srv.Close()

	svc, err := gdrive.NewService(context.Background(),
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	g := &Google{service: svc, parentFolder: "configured-folder"}
	path := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := g.Publish(context.Background(), PublishRequest{
		Name:         "x.mp4",
		Path:         path,
		ParentFolder: "override-folder",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !strings.Contains(string(body), `"parents":["override-folder"]`) {
		t.Fatalf("request parent should override the configured folder; body=%s", body)
	}
	if strings.Contains(string(body), "configured-folder") {
		t.Fatalf("configured folder leaked into request; body=%s", body)
	}
	// Empty webViewLink must fall back to the canonical URL shape.
	if res.WebViewLink != "https://drive.google.com/file/d/file-2/view" {
		t.Fatalf("webViewLink fallback = %q", res.WebViewLink)
	}
}

// driveStub returns a fixed Create response body, so the provider-side
// integrity checks can be exercised without a real Drive account.
func driveStub(t *testing.T, body string) *Google {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	svc, err := gdrive.NewService(context.Background(),
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return &Google{service: svc}
}

func writeABC(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// const abcMD5 is the MD5 of the three bytes "abc", the content used by the
// integrity tests below.
const abcMD5 = "900150983cd24fb0d6963f7d28e17f72"

// TestGooglePublishUsesProviderReportedIntegrity pins that the returned size
// and checksum come from the provider when Drive reports them: the caller's
// size comparison is then a real end-to-end assertion, not a local echo.
func TestGooglePublishUsesProviderReportedIntegrity(t *testing.T) {
	g := driveStub(t, `{"id":"file-3","size":"3","md5Checksum":"`+abcMD5+`"}`)
	res, err := g.Publish(context.Background(), PublishRequest{
		Name: "x.mp4", Path: writeABC(t), ContentType: "video/mp4"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.SizeBytes != 3 {
		t.Fatalf("SizeBytes = %d, want provider-reported 3", res.SizeBytes)
	}
	if res.MD5Checksum != abcMD5 {
		t.Fatalf("MD5Checksum = %q, want %q", res.MD5Checksum, abcMD5)
	}
}

// TestGooglePublishRejectsChecksumMismatch pins that a provider checksum that
// disagrees with the uploaded bytes fails the publication — the truncation /
// corruption detector the local-size-only check could never provide.
func TestGooglePublishRejectsChecksumMismatch(t *testing.T) {
	g := driveStub(t, `{"id":"file-4","size":"3","md5Checksum":"`+strings.Repeat("0", 32)+`"}`)
	_, err := g.Publish(context.Background(), PublishRequest{
		Name: "x.mp4", Path: writeABC(t), ContentType: "video/mp4"})
	if err == nil || !strings.Contains(err.Error(), "md5") {
		t.Fatalf("want md5 mismatch, got %v", err)
	}
}

// TestGooglePublishRejectsProviderSizeMismatch pins the size half of the same
// check: a provider that stored fewer bytes than the local file is a failure.
func TestGooglePublishRejectsProviderSizeMismatch(t *testing.T) {
	g := driveStub(t, `{"id":"file-5","size":"4"}`)
	_, err := g.Publish(context.Background(), PublishRequest{
		Name: "x.mp4", Path: writeABC(t), ContentType: "video/mp4"})
	if err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("want size mismatch, got %v", err)
	}
}
