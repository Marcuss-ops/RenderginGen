package overlay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	previewassets "github.com/Marcuss-ops/RenderingGen/renderinggen/motion-preview"
)

// The preview ledger is a pointer index over artifacts whose media is
// gitignored. These tests pin the two properties that make it safe to publish:
// every entry names a real repo-produced artifact, and a motion the ledger never
// recorded is reported as "none" instead of being implied available.

func TestPreviewLedgerParsesAndPublishesPointers(t *testing.T) {
	registry, err := previewassets.Load()
	if err != nil {
		t.Fatalf("parse embedded preview ledger: %v", err)
	}
	if registry.SchemaVersion != 1 {
		t.Errorf("ledger schema_version = %d, want 1", registry.SchemaVersion)
	}
	if registry.MediaInGit {
		t.Error("the rendered clips are gitignored; the ledger must say the media is not versioned")
	}
	if registry.GeneratedAtUTC == "" {
		t.Error("ledger has no generation timestamp, so a consumer could not tell how fresh it is")
	}
	if len(registry.Roots) == 0 {
		t.Error("ledger does not name the galleries it scanned")
	}
	if registry.Covered() == 0 {
		t.Fatal("ledger covers no motion; every published preview would be a false 'none'")
	}
	for _, entry := range registry.Entries {
		if entry.MotionID == "" || entry.Artifact == "" {
			t.Errorf("ledger entry is not a pointer: %+v", entry)
			continue
		}
		if !strings.HasSuffix(entry.Artifact, ".json") {
			t.Errorf("ledger entry %s names artifact %q, which is not a repo-produced plan/manifest", entry.MotionID, entry.Artifact)
		}
		if entry.Media != "" && !strings.HasSuffix(entry.Media, ".mp4") {
			t.Errorf("ledger entry %s names media %q, which is not a clip", entry.MotionID, entry.Media)
		}
	}
}

func TestSelectionModelPreviewCountsPartitionTheRegistry(t *testing.T) {
	model := runtimePreviewModel()
	if model.Error != "" {
		t.Fatalf("preview ledger did not parse: %s", model.Error)
	}
	if model.Source == "" || model.Rule == "" || model.GeneratedAt == "" {
		t.Errorf("preview block is not self-describing: %+v", model)
	}
	if model.MediaInGit {
		t.Error("preview media is gitignored; the model must publish that policy instead of implying shipped clips")
	}
	ids := motion.Registry.List()
	if model.Recorded+model.None != len(ids) {
		t.Errorf("preview counts %d recorded + %d none do not partition the %d registered motions", model.Recorded, model.None, len(ids))
	}
	if model.Entries < model.Recorded {
		t.Errorf("ledger has %d entries but reports %d recorded motions; one motion may appear in several galleries, never fewer", model.Entries, model.Recorded)
	}

	catalog := CompiledRuntimeAnimationCatalog()
	recorded := 0
	for _, option := range catalog.Motions {
		switch option.Preview.Status {
		case previewStatusRecorded:
			recorded++
			if option.Preview.Artifact == "" {
				t.Errorf("recorded preview for %q names no artifact, so the clip could never be regenerated", option.ID)
			}
		case previewStatusNone:
			if option.Preview.Artifact != "" || option.Preview.Media != "" {
				t.Errorf("unrecorded preview for %q still carries a pointer: %+v", option.ID, option.Preview)
			}
		default:
			t.Errorf("motion %q publishes unknown preview status %q", option.ID, option.Preview.Status)
		}
	}
	if recorded != model.Recorded {
		t.Errorf("catalog publishes %d recorded previews, the model reports %d", recorded, model.Recorded)
	}

	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	for _, key := range []string{`"preview"`, `"media_in_git"`, `"artifact"`} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("serialized payload omitted %s", key)
		}
	}
}
