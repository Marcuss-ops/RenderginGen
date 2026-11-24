// Command chainpancheck compiles a real overlay plan after selecting a chain
// camera pan, without rendering. It is a diagnostic for the chain-vs-single
// choreography: it prints each chain layer's strip position and track.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: chainpancheck <plan.json> <camera-id>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		fail(err)
	}
	// The diagnostic rewrites the chain camera; the producer fingerprint and
	// per-item render keys no longer describe the bytes, so drop them.
	delete(doc, "fingerprint")
	items, _ := doc["items"].([]any)
	chains := 0
	for _, entry := range items {
		item, _ := entry.(map[string]any)
		if item["kind"] != "image_chain" {
			continue
		}
		params, _ := item["params"].(map[string]any)
		params["chain_camera"] = os.Args[2]
		chains++
	}
	if chains == 0 {
		fail(fmt.Errorf("no image_chain item in %s", os.Args[1]))
	}
	rewritten, err := json.Marshal(doc)
	if err != nil {
		fail(err)
	}
	result, err := overlay.CompileSemantic(rewritten)
	if err != nil {
		fail(err)
	}
	dump, err := json.MarshalIndent(result.Plan, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "chainpancheck: marshal compiled plan: %v\n", err)
	} else if err := os.WriteFile("/tmp/chainpan_compiled.json", dump, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "chainpancheck: write compiled plan dump: %v\n", err)
	}
	for _, layer := range result.Plan.Layers {
		if !strings.Contains(layer.ID, "seg") {
			continue
		}
		var xTrack []string
		if layer.Animation != nil {
			for _, track := range layer.Animation.Tracks {
				if track.Property != "position_x" && track.Property != "position_y" {
					continue
				}
				for _, key := range track.Keyframes {
					xTrack = append(xTrack, fmt.Sprintf("%d=%v", key.Frame, key.Value))
				}
			}
		}
		fmt.Printf("%s type=%s pos=%v window=[%d,%d) transition=%v strip=%v track=%v\n",
			layer.ID, layer.Type, layer.Position, layer.StartFrame, layer.StartFrame+layer.DurationFrames,
			layer.TransitionIn != nil, layer.ChainPanOffset, xTrack)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "chainpancheck:", err)
	os.Exit(1)
}
