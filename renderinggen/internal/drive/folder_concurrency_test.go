package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// folderStore is a stateful Drive stub for folders. Lookups see every folder
// created so far, like the real API, and each lookup is delayed so concurrent
// callers overlap between "list" and "create" — the window the race lives in.
type folderStore struct {
	mu      sync.Mutex
	folders []string // ids of created folders, all named folderName
	creates int
}

const folderName = "boxe"

func newFolderStoreGoogle(t *testing.T, store *folderStore) *Google {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			time.Sleep(20 * time.Millisecond)
			store.mu.Lock()
			defer store.mu.Unlock()
			files := make([]map[string]any, 0, len(store.folders))
			for _, id := range store.folders {
				files = append(files, map[string]any{"id": id, "name": folderName, "parents": []string{"parent-1"}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
		case http.MethodPost:
			_, _ = io.ReadAll(r.Body)
			store.mu.Lock()
			store.creates++
			id := fmt.Sprintf("folder-%d", store.creates)
			store.folders = append(store.folders, id)
			store.mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
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

// TestGoogleEnsureFolderConcurrentCallersCreateOneFolder pins the race fix:
// eight publications resolving the same new folder must issue exactly one
// create and all receive that folder's id. Without the folder lock, several
// callers list "not found" before any create lands and each creates a folder.
func TestGoogleEnsureFolderConcurrentCallersCreateOneFolder(t *testing.T) {
	store := &folderStore{}
	g := newFolderStoreGoogle(t, store)

	const callers = 8
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = g.EnsureFolder(context.Background(), "parent-1", folderName)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: EnsureFolder: %v", i, err)
		}
	}
	store.mu.Lock()
	creates := store.creates
	store.mu.Unlock()
	if creates != 1 {
		t.Fatalf("Drive folder creates = %d, want exactly 1 (duplicate folders created)", creates)
	}
	for i, id := range ids {
		if id != ids[0] {
			t.Fatalf("caller %d got folder %q, caller 0 got %q: one folder must resolve to one id", i, id, ids[0])
		}
	}
}
