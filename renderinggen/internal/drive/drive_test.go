package drive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	res, err := g.Publish(context.Background(), PublishRequest{
		Name:        "x.mp4",
		ContentType: "video/mp4",
		Data:        []byte("abc"),
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
	res, err := g.Publish(context.Background(), PublishRequest{
		Name:         "x.mp4",
		Data:         []byte("abc"),
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
