package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The curated background fixtures live at the module root, not under
// internal/, so the test walks up three levels the same way the other
// RenderingGen fixtures do (testdata/golden in internal/chronon and
// internal/media use the identical relative root).
const (
	backgroundManifestRel = "../../../assets/backgrounds/manifest.json"
	backgroundReadmeRel   = "../../../assets/backgrounds/README.md"
)

// backgroundManifest mirrors assets/backgrounds/manifest.json. It is declared
// here rather than in production code on purpose: no Go path consumes this
// catalog yet, so inventing a loadable registry would be a second owner for a
// decision that has not been made. What the test DOES enforce is that the
// three places which already describe these assets — the manifest, the README
// example, and the bytes on disk — cannot drift apart silently.
type backgroundManifest struct {
	Version  int    `json:"version"`
	Kind     string `json:"kind"`
	Contract string `json:"contract"`
	Canvas   struct {
		Width  int `json:"width"`
		Height int `json:"height"`
		FPS    int `json:"fps"`
	} `json:"canvas"`
	DurationSeconds int `json:"duration_seconds"`
	AudioStreams    int `json:"audio_streams"`
	Assets          []struct {
		ID                string `json:"id"`
		File              string `json:"file"`
		SHA256            string `json:"sha256"`
		SourceDriveFileID string `json:"source_drive_file_id"`
		MediaType         string `json:"media_type"`
		Role              string `json:"role"`
	} `json:"assets"`
}

func loadBackgroundManifest(t *testing.T) (backgroundManifest, string) {
	t.Helper()
	path := filepath.Clean(backgroundManifestRel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("background manifest unreadable: %v", err)
	}
	var manifest backgroundManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("background manifest is not valid json: %v", err)
	}
	return manifest, filepath.Dir(path)
}

var (
	sha256Token  = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	assetIDToken = regexp.MustCompile(`\bdrive-background-[0-9]{2}\b`)
)

func TestBackgroundManifestIsStructurallyValid(t *testing.T) {
	manifest, _ := loadBackgroundManifest(t)

	if manifest.Kind != "renderinggen.background-assets" {
		t.Errorf("kind = %q, want renderinggen.background-assets", manifest.Kind)
	}
	if manifest.Contract != "video-background-v1" {
		t.Errorf("contract = %q, want video-background-v1", manifest.Contract)
	}
	if manifest.Canvas.Width != 1920 || manifest.Canvas.Height != 1080 || manifest.Canvas.FPS != 30 {
		t.Errorf("canvas = %dx%d@%d, want 1920x1080@30", manifest.Canvas.Width, manifest.Canvas.Height, manifest.Canvas.FPS)
	}
	if manifest.DurationSeconds != 15 {
		t.Errorf("duration_seconds = %d, want 15", manifest.DurationSeconds)
	}
	// The whole point of these plates is that they carry no audio of their
	// own: they run under the Chronon VIDEO_BACKGROUND layer while the master
	// voiceover/BGM contract owns the audio timeline.
	if manifest.AudioStreams != 0 {
		t.Errorf("audio_streams = %d, want 0 (a plate with audio would double the master audio)", manifest.AudioStreams)
	}
	if len(manifest.Assets) == 0 {
		t.Fatal("manifest declares no background assets")
	}

	seenID := make(map[string]string, len(manifest.Assets))
	seenFile := make(map[string]string, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		if prior, dup := seenID[asset.ID]; dup {
			t.Errorf("asset id %q declared twice (also as %q)", asset.ID, prior)
		}
		seenID[asset.ID] = asset.File
		if prior, dup := seenFile[asset.File]; dup {
			t.Errorf("file %q declared twice (also for %q)", asset.File, prior)
		}
		seenFile[asset.File] = asset.ID

		if len(asset.SHA256) != 64 || strings.ToLower(asset.SHA256) != asset.SHA256 {
			t.Errorf("asset %s: sha256 %q must be 64 lowercase hex characters", asset.ID, asset.SHA256)
			continue
		}
		if _, err := hex.DecodeString(asset.SHA256); err != nil {
			t.Errorf("asset %s: sha256 %q is not hex: %v", asset.ID, asset.SHA256, err)
		}
		if asset.SourceDriveFileID == "" {
			t.Errorf("asset %s: source_drive_file_id is required (it is the only link back to the original)", asset.ID)
		}
		if asset.MediaType != "video/mp4" || !strings.HasSuffix(asset.File, ".mp4") {
			t.Errorf("asset %s: media_type %q / file %q disagree with the video/mp4 contract", asset.ID, asset.MediaType, asset.File)
		}
	}
}

// TestBackgroundFixtureBytesMatchTheirManifestHash verifies the content
// address of every plate that IS checked in. Missing plates are not an error —
// the README is explicit that the manifest documents hashes for files that
// production jobs upload, not a guarantee that every fixture is vendored — but
// a present file whose bytes disagree with the declared hash is.
func TestBackgroundFixtureBytesMatchTheirManifestHash(t *testing.T) {
	manifest, dir := loadBackgroundManifest(t)

	var verified int
	for _, asset := range manifest.Assets {
		path := filepath.Join(dir, asset.File)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Errorf("asset %s: read %s: %v", asset.ID, path, err)
			continue
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != asset.SHA256 {
			t.Errorf("asset %s: %s hashes to %s, manifest declares %s", asset.ID, asset.File, got, asset.SHA256)
		}
		verified++
	}
	if verified == 0 {
		t.Skip("no background fixture is checked in")
	}
}

// TestBackgroundReadmeQuotesOnlyManifestFacts closes the one duplication in
// this directory: README.md carries a worked example with a hardcoded asset id
// and sha256, and states the canvas/duration/audio invariants in prose. Both
// are copies of manifest.json. Nothing else in the repository compares them,
// so the example silently becomes wrong the moment a plate is re-normalized.
func TestBackgroundReadmeQuotesOnlyManifestFacts(t *testing.T) {
	manifest, dir := loadBackgroundManifest(t)

	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Skipf("background README unreadable: %v", err)
	}
	text := string(readme)

	declaredHashes := make(map[string]struct{}, len(manifest.Assets))
	declaredIDs := make(map[string]struct{}, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		declaredHashes[asset.SHA256] = struct{}{}
		declaredIDs[asset.ID] = struct{}{}
	}

	quotedHashes := sha256Token.FindAllString(text, -1)
	if len(quotedHashes) == 0 {
		t.Error("README.md quotes no content hash; the example must publish the manifest hash or this check is vacuous")
	}
	for _, hash := range quotedHashes {
		if _, ok := declaredHashes[hash]; !ok {
			t.Errorf("README.md quotes sha256 %s, which is not declared in manifest.json", hash)
		}
	}
	quotedIDs := assetIDToken.FindAllString(text, -1)
	if len(quotedIDs) == 0 {
		t.Error("README.md no longer shows a drive-background-* example; if the example moved, move this check with it")
	}
	for _, id := range quotedIDs {
		if _, ok := declaredIDs[id]; !ok {
			t.Errorf("README.md references %q, which is not declared in manifest.json", id)
		}
	}

	invariants := []string{
		fmt.Sprintf("%d×%d", manifest.Canvas.Width, manifest.Canvas.Height),
		fmt.Sprintf("%d fps", manifest.Canvas.FPS),
		fmt.Sprintf("%d seconds", manifest.DurationSeconds),
		fmt.Sprintf("zero audio streams"),
	}
	for _, want := range invariants {
		if !strings.Contains(text, want) {
			t.Errorf("README.md no longer states %q; the prose must stay derivable from manifest.json", want)
		}
	}
}
