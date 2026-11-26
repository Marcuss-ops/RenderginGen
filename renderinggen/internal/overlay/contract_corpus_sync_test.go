package overlay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestContractCorpusMatchesWorkspaceSources keeps the vendored snapshot honest
// when the full VeloxEditing workspace is checked out. RenderingGen-only CI has
// no sibling `refactored/` tree and no parent `contracts/` directory, so the
// test skips there instead of turning a hermetic checkout into a failure.
//
// The source of truth remains the workspace corpus: this test compares the
// fixture bytes and the pinned digests, not just the manifest metadata, because
// a producer can change a plan without changing its ID.
func TestContractCorpusMatchesWorkspaceSources(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Dir(repoRoot)
	if info, err := os.Stat(filepath.Join(workspace, "refactored")); err != nil || !info.IsDir() {
		t.Skip("PipelineGen workspace checkout is not available; vendored corpus cannot be compared")
	}

	outerPath := filepath.Join(workspace, "contracts", "overlay-v2", "manifest.json")
	outerRaw, err := os.ReadFile(outerPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("workspace overlay corpus is not available; vendored corpus cannot be compared")
		}
		t.Fatalf("read workspace manifest %s: %v", outerPath, err)
	}
	localPath := filepath.Join(repoRoot, "contracts", "overlay-v2", "manifest.json")
	localRaw, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read vendored manifest %s: %v", localPath, err)
	}

	var outer, local contractCorpusManifest
	if err := json.Unmarshal(outerRaw, &outer); err != nil {
		t.Fatalf("decode workspace manifest: %v", err)
	}
	if err := json.Unmarshal(localRaw, &local); err != nil {
		t.Fatalf("decode vendored manifest: %v", err)
	}
	if len(local.Fixtures) != len(outer.Fixtures) {
		t.Fatalf("vendored corpus has %d fixtures, workspace has %d", len(local.Fixtures), len(outer.Fixtures))
	}

	localByID := make(map[string]contractCorpusFixture, len(local.Fixtures))
	for _, fixture := range local.Fixtures {
		localByID[fixture.ID] = fixture
	}
	for _, want := range outer.Fixtures {
		got, ok := localByID[want.ID]
		if !ok {
			t.Errorf("workspace fixture %q is missing from the vendored corpus", want.ID)
			continue
		}
		if got.Expect != want.Expect || got.Layers != want.Layers ||
			got.LayersSHA256 != want.LayersSHA256 || got.ErrorContains != want.ErrorContains {
			t.Errorf("vendored fixture %q metadata is stale: got expect=%q layers=%d sha=%q error=%q, want expect=%q layers=%d sha=%q error=%q",
				want.ID, got.Expect, got.Layers, got.LayersSHA256, got.ErrorContains,
				want.Expect, want.Layers, want.LayersSHA256, want.ErrorContains)
		}

		outerSource := filepath.Join(workspace, filepath.FromSlash(want.Source))
		localSource := filepath.Join(repoRoot, filepath.FromSlash(got.Source))
		outerBytes, err := os.ReadFile(outerSource)
		if err != nil {
			t.Fatalf("read workspace fixture %q (%s): %v", want.ID, outerSource, err)
		}
		localBytes, err := os.ReadFile(localSource)
		if err != nil {
			t.Fatalf("read vendored fixture %q (%s): %v", want.ID, localSource, err)
		}
		if !bytes.Equal(outerBytes, localBytes) {
			t.Errorf("vendored fixture %q is stale: copy %s to %s", want.ID, outerSource, localSource)
		}
	}
}
