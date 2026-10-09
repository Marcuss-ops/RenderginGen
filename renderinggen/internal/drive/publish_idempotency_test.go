package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// uploadStore is a stateful Drive stub for file publication. Listing returns
// the stored files; a create (multipart upload) stores the file under the name
// from its metadata and counts as one upload.
type uploadStore struct {
	mu      sync.Mutex
	files   []map[string]any
	uploads int
}

const uploadName = "x.mp4"

func newUploadStoreGoogle(t *testing.T, store *uploadStore) *Google {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			store.mu.Lock()
			defer store.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"files": store.files})
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			store.mu.Lock()
			defer store.mu.Unlock()
			store.uploads++
			id := "uploaded-" + string(rune('0'+store.uploads))
			f := map[string]any{"id": id, "name": uploadName, "parents": []string{"parent-1"},
				"size": "3", "md5Checksum": abcMD5}
			if !strings.Contains(string(body), `"name":"`+uploadName+`"`) {
				http.Error(w, "unexpected upload metadata", http.StatusBadRequest)
				return
			}
			store.files = append(store.files, f)
			_ = json.NewEncoder(w).Encode(f)
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	svc, err := gdrive.NewService(context.Background(),
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return &Google{service: svc}
}

func publishABC(t *testing.T, g *Google) Result {
	t.Helper()
	res, err := g.Publish(context.Background(), PublishRequest{
		Name: uploadName, Path: writeABC(t), ContentType: "video/mp4", ParentFolder: "parent-1"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return res
}

// TestPublishReusesAnIdenticalExistingFile pins the retry case: the file is
// already there (the earlier upload succeeded but its response was lost), so a
// new publication must return it and upload nothing.
func TestPublishReusesAnIdenticalExistingFile(t *testing.T) {
	store := &uploadStore{files: []map[string]any{
		{"id": "existing", "name": uploadName, "parents": []string{"parent-1"}, "size": "3", "md5Checksum": abcMD5},
	}}
	g := newUploadStoreGoogle(t, store)

	res := publishABC(t, g)
	if store.uploads != 0 {
		t.Fatalf("uploads = %d, want 0: an identical file already exists", store.uploads)
	}
	if res.FileID != "existing" {
		t.Fatalf("FileID = %q, want the existing file", res.FileID)
	}
	if res.MD5Checksum != abcMD5 || res.SizeBytes != 3 {
		t.Fatalf("result integrity = md5 %q size %d, want the existing file's", res.MD5Checksum, res.SizeBytes)
	}
}

// TestPublishUploadsWhenTheSameNameHoldsDifferentBytes pins the other side: a
// file with the same name in the same folder but different content is not the
// publication, so the new bytes must be uploaded.
func TestPublishUploadsWhenTheSameNameHoldsDifferentBytes(t *testing.T) {
	store := &uploadStore{files: []map[string]any{
		{"id": "stale", "name": uploadName, "parents": []string{"parent-1"}, "size": "9",
			"md5Checksum": strings.Repeat("0", 32)},
	}}
	g := newUploadStoreGoogle(t, store)

	res := publishABC(t, g)
	if store.uploads != 1 {
		t.Fatalf("uploads = %d, want 1: the stored file has different bytes", store.uploads)
	}
	if res.FileID == "stale" {
		t.Fatal("publication reused a file with different content")
	}
}

// TestPublishRetryAfterALostResponseCreatesOnce drives the whole retry: the
// first publication uploads, the second (same bytes, same name and folder)
// must resolve to that same file without a second upload.
func TestPublishRetryAfterALostResponseCreatesOnce(t *testing.T) {
	store := &uploadStore{}
	g := newUploadStoreGoogle(t, store)

	first := publishABC(t, g)
	second := publishABC(t, g)
	if store.uploads != 1 {
		t.Fatalf("uploads = %d after a retry, want exactly 1", store.uploads)
	}
	if second.FileID != first.FileID {
		t.Fatalf("retry resolved to %q, want the first upload %q", second.FileID, first.FileID)
	}
}
