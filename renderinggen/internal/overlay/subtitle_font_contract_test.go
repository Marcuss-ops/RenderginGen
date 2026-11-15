package overlay

import (
	"strings"
	"testing"
)

const testSubtitleFontSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// TestSubtitleAssetCarriesDeclaredFontHash pins the typography handoff on the
// worker side: a burn plan that declares font_ref must expose that digest, and
// a plan that predates the field must stay readable with an empty font hash
// (the first-materialized font fallback is the legacy path only).
func TestSubtitleAssetCarriesDeclaredFontHash(t *testing.T) {
	subtitleSHA := strings.Repeat("b", 64)
	withFont := `{"width":1920,"height":1080,"subtitles":{"mode":"burn","asset_refs":[{"asset_id":"ass","sha256":"` + subtitleSHA + `","url":"assets/semantic/ass/subtitles.ass","media_type":"text/x-ass"}],"font_ref":{"asset_id":"font-inter","sha256":"` + testSubtitleFontSHA + `","url":"assets/semantic/font-inter/Inter.ttf","media_type":"font/ttf"},"style":{}}}`
	hash, fontHash, burn, ok, err := SubtitleAsset([]byte(withFont))
	if err != nil {
		t.Fatalf("SubtitleAsset: %v", err)
	}
	if !ok || !burn || hash != subtitleSHA || fontHash != testSubtitleFontSHA {
		t.Fatalf("hash=%q fontHash=%q burn=%v ok=%v, want %q/%q/burn", hash, fontHash, burn, ok, subtitleSHA, testSubtitleFontSHA)
	}

	legacy := `{"width":1920,"height":1080,"subtitles":{"mode":"burn","asset_refs":[{"asset_id":"ass","sha256":"` + subtitleSHA + `","url":"assets/semantic/ass/subtitles.ass","media_type":"text/x-ass"}],"style":{}}}`
	_, fontHash, burn, ok, err = SubtitleAsset([]byte(legacy))
	if err != nil {
		t.Fatalf("SubtitleAsset legacy: %v", err)
	}
	if !ok || !burn || fontHash != "" {
		t.Fatalf("legacy fontHash=%q burn=%v ok=%v, want empty/burn", fontHash, burn, ok)
	}
}
