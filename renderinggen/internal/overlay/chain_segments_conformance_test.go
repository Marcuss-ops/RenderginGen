package overlay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const conformanceFixtureSHA256 = "c8b8195de94e39b3ff36ffba938f4941a3e929ce0bdb1fdd0c8b37e445088faa"

// TestSharedChainSegmentsFixtureCompilesFiveImages is the consumer half of the
// cross-repository chain_segments contract. The same bytes PipelineGen
// validates must compile here into five distinct image layers, each carrying
// its own asset and motion.
func TestSharedChainSegmentsFixtureCompilesFiveImages(t *testing.T) {
	path := filepath.Join("..", "..", "..", "contracts", "chain-segments-v1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared conformance fixture: %v", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != conformanceFixtureSHA256 {
		t.Fatalf("shared fixture sha256 = %s, want %s; update both repositories together", got, conformanceFixtureSHA256)
	}
	var document struct {
		Plan json.RawMessage `json:"plan"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode conformance document: %v", err)
	}
	result, err := CompileSemantic(document.Plan)
	if err != nil {
		t.Fatalf("consumer rejected the shared conformance plan: %v", err)
	}
	assetsBySegment := map[string]string{}
	for _, layer := range result.Plan.Layers {
		if layer.Type != "image" {
			continue
		}
		for index := 0; index < 5; index++ {
			if strings.Contains(layer.ID, fmt.Sprintf(":seg%d", index)) {
				if previous, ok := assetsBySegment[fmt.Sprintf("seg%d", index)]; ok && previous != layer.Asset {
					t.Fatalf("segment %d produced conflicting assets %q and %q", index, previous, layer.Asset)
				}
				assetsBySegment[fmt.Sprintf("seg%d", index)] = layer.Asset
				break
			}
		}
	}
	if len(assetsBySegment) != 5 {
		t.Fatalf("consumer lowered %d distinct image segments, want 5: %+v", len(assetsBySegment), assetsBySegment)
	}
	seen := map[string]bool{}
	for segment, asset := range assetsBySegment {
		if asset == "" {
			t.Fatalf("%s has no image asset", segment)
		}
		if seen[asset] {
			t.Fatalf("%s reuses asset %q; a composite was produced", segment, asset)
		}
		seen[asset] = true
	}
}
