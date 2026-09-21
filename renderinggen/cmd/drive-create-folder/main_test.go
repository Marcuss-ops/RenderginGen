package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
)

// TestCreateAndVerify_MockPublisherIsDeterministic is the local e2e of the
// command's decision function: through the Mock (the same port the Google
// client implements) the get-or-create rule is observable — the same pair
// yields the same id and the folder exists on disk — and the name is trimmed
// before it becomes a folder.
func TestCreateAndVerify_MockPublisherIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	creator := drive.NewMock(dir, 0)

	first, err := createAndVerify(context.Background(), creator, "parent-1", " TeamCoco ")
	if err != nil {
		t.Fatalf("createAndVerify: %v", err)
	}
	second, err := createAndVerify(context.Background(), creator, "parent-1", "TeamCoco")
	if err != nil {
		t.Fatalf("second createAndVerify: %v", err)
	}
	if first != second {
		t.Fatalf("ids differ for the same folder: %q vs %q (get-or-create must be idempotent)", first, second)
	}
	if first != "mock-folder-parent-1-TeamCoco" {
		t.Fatalf("id = %q, want the deterministic mock id", first)
	}
	if _, err := os.Stat(filepath.Join(dir, "TeamCoco")); err != nil {
		t.Fatalf("folder was not materialised: %v", err)
	}
}

// emptyIDCreator is a FolderCreator that answers success without a handle —
// the lie createAndVerify exists to catch.
type emptyIDCreator struct {
	calls int
}

func (e *emptyIDCreator) EnsureFolder(_ context.Context, _, _ string) (string, error) {
	e.calls++
	return "", nil
}

// TestCreateAndVerify_RejectsEmptyProviderID pins the falsifiable half: a
// provider reporting success with an empty id must abort, because the PASS line
// would point at a URL that resolves to nothing.
func TestCreateAndVerify_RejectsEmptyProviderID(t *testing.T) {
	p := &emptyIDCreator{}
	_, err := createAndVerify(context.Background(), p, "parent-1", "boxe")
	if err == nil || !strings.Contains(err.Error(), "empty folder id") {
		t.Fatalf("err = %v, want an empty-id failure", err)
	}
	if p.calls != 1 {
		t.Fatalf("provider called %d time(s), want 1", p.calls)
	}
}

// TestCreateAndVerify_RejectsBlankName pins that a whitespace-only name never
// reaches the provider: a folder named "   " would be invisible in the Drive
// web UI but real on the API.
func TestCreateAndVerify_RejectsBlankName(t *testing.T) {
	p := &emptyIDCreator{}
	if _, err := createAndVerify(context.Background(), p, "parent-1", "   "); err == nil || !strings.Contains(err.Error(), "folder name is required") {
		t.Fatalf("err = %v, want a name failure", err)
	}
	if p.calls != 0 {
		t.Fatalf("provider called %d time(s), want 0 for a rejected name", p.calls)
	}
}

// TestFolderURL pins the canonical link shape the PASS line prints.
func TestFolderURL(t *testing.T) {
	if got, want := folderURL("abc123"), "https://drive.google.com/drive/folders/abc123"; got != want {
		t.Fatalf("folderURL = %q, want %q", got, want)
	}
}

// TestCreateAndVerify_PropagatesProviderError pins that a refused creation is
// surfaced verbatim instead of being swallowed into a fake PASS.
func TestCreateAndVerify_PropagatesProviderError(t *testing.T) {
	failing := &failingFolderCreator{err: fmt.Errorf("google drive refused: 403 forbidden")}
	if _, err := createAndVerify(context.Background(), failing, "parent-1", "boxe"); err == nil || !strings.Contains(err.Error(), "403 forbidden") {
		t.Fatalf("err = %v, want the provider error surfaced", err)
	}
}

type failingFolderCreator struct {
	err error
}

func (f *failingFolderCreator) EnsureFolder(context.Context, string, string) (string, error) {
	return "", f.err
}
