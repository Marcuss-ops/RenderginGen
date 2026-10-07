// Package previewassets exposes the checked-in preview-asset ledger as a
// machine-readable source for the runtime animation catalog.
//
// Every rendered preview clip in this repository is deliberately kept out of git
// (see .gitignore: the mp4/timing/plan families are regenerated on demand), so
// the catalog cannot publish a video path as if it were a shipped asset. What it
// CAN publish, per motion, is the ledger entry: the repo-produced artifact that
// declares the preview and, when the gallery names it mechanically, the media
// path. registry.v1.json is written by cmd/preview-registry, which scans the
// gallery and batch artifacts; the entry is a POINTER, and a consumer must
// regenerate the media from the artifact before showing it.
package previewassets

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
)

// registryFS holds the ledger itself, so the embedded bytes cannot point at a
// stale copy elsewhere in the tree.
//
//go:embed registry.v1.json
var registryFS embed.FS

// Entry is the recorded preview of one motion.
type Entry struct {
	MotionID string `json:"motion_id"`
	// Artifact is the repo-produced JSON that declares the preview plan.
	Artifact string `json:"artifact"`
	// Media is the rendered clip path when the gallery names it mechanically.
	// Empty means the ledger knows the plan but not the clip: the collector
	// never guesses a media path.
	Media string `json:"media,omitempty"`
}

// Registry is the whole ledger.
type Registry struct {
	SchemaVersion  int    `json:"schema_version"`
	GeneratedAtUTC string `json:"generated_at_utc"`
	// Roots are the scanned galleries, so a consumer can tell what the ledger
	// did and did not look at.
	Roots []string `json:"roots"`
	// MediaInGit states the .gitignore policy the ledger depends on: the clips
	// are not versioned, only the pointer is.
	MediaInGit bool    `json:"media_in_git"`
	Entries    []Entry `json:"entries"`
}

// Load parses the embedded ledger. An invalid or unsupported schema is an error
// rather than an empty registry: publishing "no preview for any motion" because
// the file could not be read is exactly the silent failure this ledger exists to
// avoid.
func Load() (Registry, error) {
	raw, err := registryFS.ReadFile("registry.v1.json")
	if err != nil {
		return Registry{}, fmt.Errorf("previewassets: read embedded registry: %w", err)
	}
	var registry Registry
	if err := json.Unmarshal(raw, &registry); err != nil {
		return Registry{}, fmt.Errorf("previewassets: decode registry: %w", err)
	}
	if registry.SchemaVersion != 1 {
		return Registry{}, fmt.Errorf("previewassets: unsupported registry schema_version %d, want 1", registry.SchemaVersion)
	}
	sort.SliceStable(registry.Entries, func(i, j int) bool {
		if registry.Entries[i].MotionID != registry.Entries[j].MotionID {
			return registry.Entries[i].MotionID < registry.Entries[j].MotionID
		}
		return registry.Entries[i].Artifact < registry.Entries[j].Artifact
	})
	return registry, nil
}

// EntryFor returns the first recorded preview of one motion. The ledger is
// sorted, so the answer does not depend on the file order.
func (r Registry) EntryFor(motionID string) (Entry, bool) {
	index := sort.Search(len(r.Entries), func(i int) bool { return r.Entries[i].MotionID >= motionID })
	if index < len(r.Entries) && r.Entries[index].MotionID == motionID {
		return r.Entries[index], true
	}
	return Entry{}, false
}

// Covered counts the motions with a recorded preview.
func (r Registry) Covered() int {
	seen := make(map[string]struct{}, len(r.Entries))
	for _, entry := range r.Entries {
		seen[entry.MotionID] = struct{}{}
	}
	return len(seen)
}
