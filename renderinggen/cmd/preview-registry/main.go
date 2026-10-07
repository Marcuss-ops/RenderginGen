// Command preview-registry scans the repo-produced gallery and batch artifacts
// and writes the checked-in preview ledger the runtime catalog embeds.
//
// The ledger is a POINTER INDEX, not the media: every rendered preview clip is
// gitignored and regenerated on demand, so what can be versioned is where the
// preview was produced and — when the gallery's own naming rule makes it
// unambiguous — which clip belongs to which motion. The scanner never guesses a
// media path: an artifact that does not name one leaves the media field empty,
// and the catalog publishes the plan without claiming a clip exists.
//
// Run it from the module root:
//
//	go run ./cmd/preview-registry -root .. -output motion-preview/registry.v1.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	previewassets "github.com/Marcuss-ops/RenderingGen/renderinggen/motion-preview"
)

// defaultRoots are the top-level artifact roots the gallery commands write. A
// missing root is not an error: the ledger records what exists in the checkout.
const defaultRoots = "out,preset_videos,phrase_preset_videos,typewriter_phrase_videos,apple_style_final_videos"

// brushGallerySchema is the one artifact that carries an explicit motion_id plus
// the plan it rendered, which is what lets the scanner resolve a media path.
const brushGallerySchema = "renderinggen.brush-phrase-gallery.v1"

func main() {
	root := flag.String("root", ".", "repository root the scan runs from")
	output := flag.String("output", filepath.Join("motion-preview", "registry.v1.json"), "ledger path, relative to the working directory")
	roots := flag.String("roots", defaultRoots, "comma-separated preview roots, relative to the repo root")
	flag.Parse()

	registry, err := collect(*root, strings.Split(*roots, ","))
	if err != nil {
		fmt.Fprintln(os.Stderr, "preview-registry:", err)
		os.Exit(1)
	}

	// The output path is relative to the working directory (the module root),
	// not to the scanned tree: the ledger lives with the package that embeds it.
	path := *output
	raw, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "preview-registry: encode ledger:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "preview-registry: write ledger:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "preview-registry: %d motions covered, %d entries -> %s\n", registry.Covered(), len(registry.Entries), path)
}

// collect walks the preview roots and returns the ledger. Entries are deduped by
// (motion, artifact) and sorted, so a rerun on the same checkout is byte-stable
// apart from generated_at_utc.
func collect(root string, roots []string) (previewassets.Registry, error) {
	registry := previewassets.Registry{
		SchemaVersion:  1,
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		MediaInGit:     false,
	}

	type key struct{ motion, artifact string }
	media := make(map[key]string)
	for _, scanRoot := range roots {
		scanRoot = strings.TrimSpace(scanRoot)
		if scanRoot == "" {
			continue
		}
		dir := filepath.Join(root, scanRoot)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		registry.Roots = append(registry.Roots, filepath.ToSlash(scanRoot))

		walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".json") || isSidecar(path) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			artifact := filepath.ToSlash(rel)
			found, err := scanArtifact(path)
			if err != nil {
				// An unparseable artifact is skipped, not fatal: a stale or
				// truncated sidecar must not block the rest of the ledger.
				return nil
			}
			for motion, mediaPath := range found {
				if mediaPath != "" {
					if relative, err := filepath.Rel(root, mediaPath); err == nil {
						mediaPath = filepath.ToSlash(relative)
					}
				}
				k := key{motion: motion, artifact: artifact}
				if mediaPath != "" || media[k] == "" {
					media[k] = mediaPath
				}
			}
			return nil
		})
		if walkErr != nil {
			return registry, fmt.Errorf("scan %s: %w", scanRoot, walkErr)
		}
	}

	registry.Entries = make([]previewassets.Entry, 0, len(media))
	for k, mediaPath := range media {
		registry.Entries = append(registry.Entries, previewassets.Entry{MotionID: k.motion, Artifact: k.artifact, Media: mediaPath})
	}
	sort.Slice(registry.Entries, func(i, j int) bool {
		if registry.Entries[i].MotionID != registry.Entries[j].MotionID {
			return registry.Entries[i].MotionID < registry.Entries[j].MotionID
		}
		return registry.Entries[i].Artifact < registry.Entries[j].Artifact
	})
	return registry, nil
}

// scanArtifact returns the motion ids one artifact declares, mapped to the
// absolute media path it names when one can be resolved without guessing.
func scanArtifact(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}

	ids := map[string]bool{}
	collectMotionIDs(decoded, ids)
	found := make(map[string]string, len(ids))
	for id := range ids {
		found[id] = ""
	}

	if decoded["schema"] == brushGallerySchema {
		entries, _ := decoded["entries"].([]any)
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			motionID, _ := entry["motion_id"].(string)
			plan, _ := entry["plan"].(string)
			if motionID == "" {
				continue
			}
			if video, _ := entry["video"].(string); video != "" {
				found[motionID] = filepath.Join(filepath.Dir(path), video)
				continue
			}
			if media := siblingMedia(path, plan); media != "" {
				found[motionID] = media
			}
		}
	}
	return found, nil
}

// siblingMedia resolves the clip that sits beside a gallery plan. The gallery
// writes <stem>.plan.json and <stem>.mp4 in the same directory, so the mapping
// is exact — the file must exist, or nothing is recorded.
func siblingMedia(manifestPath, plan string) string {
	if plan == "" || !strings.HasSuffix(plan, ".plan.json") {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(manifestPath), strings.TrimSuffix(plan, ".plan.json")+".mp4")
	if info, err := os.Stat(candidate); err != nil || info.IsDir() {
		return ""
	}
	return candidate
}

// collectMotionIDs gathers every motion the document declares, at any depth:
// galleries name a single motion_id, batch manifests carry one per job, and a
// planned composition may carry a list.
func collectMotionIDs(node any, out map[string]bool) {
	switch value := node.(type) {
	case map[string]any:
		for name, item := range value {
			switch name {
			case "motion_id":
				if id, ok := item.(string); ok && id != "" {
					out[id] = true
				}
			case "motion_ids":
				if list, ok := item.([]any); ok {
					for _, entry := range list {
						if id, ok := entry.(string); ok && id != "" {
							out[id] = true
						}
					}
				}
			}
			collectMotionIDs(item, out)
		}
	case []any:
		for _, item := range value {
			collectMotionIDs(item, out)
		}
	}
}

// isSidecar skips per-frame telemetry and timing files: they are diagnostics of
// a run, never a declaration of what was rendered.
func isSidecar(path string) bool {
	for _, suffix := range []string{".timing.json", ".telemetry-summary.json", ".receipt.json"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}
