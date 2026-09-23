// Command mixed-overlay-scenario defines and compiles ONE runtime scenario: a
// single semantic overlay plan carrying two phrase overlays and two
// entity-image overlays, in disjoint time windows over a flat background plate.
//
// It writes three artefacts into -out-dir:
//
//	semantic_plan.json  the renderinggen.overlay-plan.v1 document (input of record)
//	render_plan.json    the compiled chronon.render-plan.v2 the renderer consumes
//	scenario.json       the runtime descriptor (windows, sampled frames,
//	                    thresholds, gap frames) the verification script drives
//
// The scenario definition lives HERE, once: the script renders and probes
// pixels, but it never restates a window or a frame number, so the two halves
// cannot drift apart. Both documents are built through renderbatch (the typed
// writer) and lowered through the worker's own compiler — no hand-rolled JSON
// and no second lowering.
//
// phrase_default is deliberately not used: the phrase items render with
// static_text_smoke, the text preset certified on the software lane.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

const (
	scenarioPlanID   = "mixed-scenario"
	scenarioWidth    = 1920
	scenarioHeight   = 1080
	scenarioFPS      = 24
	scenarioDuration = 10000
	// entryFraction is where inside a window the pairwise "which preset ran"
	// probe samples: early enough that a settled portrait is still entering.
	entryFraction = 0.25
)

// scenarioItem is one overlay of the scenario plus the probe contract the
// verification script enforces for it.
type scenarioItem struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"` // phrase | entity_image
	Preset        string `json:"preset"`
	StartMS       int64  `json:"start_ms"`
	EndMS         int64  `json:"end_ms"`
	MidFrame      int    `json:"mid_frame"`
	EntryFrame    int    `json:"entry_frame"`
	MinDiffPixels int    `json:"min_diff_pixels"`
	CenterOnly    bool   `json:"center_only"`
	Text          string `json:"text,omitempty"`
	EntityID      string `json:"entity_id,omitempty"`
	Asset         string `json:"asset,omitempty"` // logical path relative to -assets-root
}

// scenarioDescriptor is what the script reads; it must be self-contained.
type scenarioDescriptor struct {
	PlanID       string         `json:"plan_id"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	FPS          int            `json:"fps"`
	DurationMS   int64          `json:"duration_ms"`
	SemanticPlan string         `json:"semantic_plan"`
	RenderPlan   string         `json:"render_plan"`
	GapFrames    []int          `json:"gap_frames"`
	Items        []scenarioItem `json:"items"`
}

// The scenario. Windows are disjoint so a change in one window can only come
// from that item, and an overlay-free gap follows each window so a held frame
// can be told apart from a leaked one.
var scenarioItems = []scenarioItem{
	{Name: "phrase_a", Kind: "phrase", Preset: overlay.StaticTextSmokePresetID,
		StartMS: 400, EndMS: 2000, MinDiffPixels: 300,
		Text: "Prima frase certificata"},
	{Name: "entity_a", Kind: "entity_image", Preset: "image_scale_in",
		StartMS: 2600, EndMS: 5200, MinDiffPixels: 20000, CenterOnly: true,
		EntityID: "person:alpha", Asset: "assets/semantic/ent-alpha.jpg"},
	{Name: "phrase_b", Kind: "phrase", Preset: overlay.StaticTextSmokePresetID,
		StartMS: 5600, EndMS: 7200, MinDiffPixels: 300,
		Text: "Seconda frase certificata"},
	{Name: "entity_b", Kind: "entity_image", Preset: "image_slide_left",
		StartMS: 7600, EndMS: 9800, MinDiffPixels: 20000, CenterOnly: true,
		EntityID: "person:beta", Asset: "assets/semantic/ent-beta.jpg"},
}

func msToFrame(ms int64) int { return int(ms * scenarioFPS / 1000) }

func frameAt(startMS, endMS int64, fraction float64) int {
	return msToFrame(startMS + int64(float64(endMS-startMS)*fraction))
}

func main() {
	assetsRoot := flag.String("assets-root", "", "workspace the plan's logical asset paths resolve against (required)")
	outDir := flag.String("out-dir", "", "directory the plan documents and the scenario descriptor are written to (required)")
	flag.Parse()
	if *assetsRoot == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "mixed-overlay-scenario: -assets-root and -out-dir are required")
		os.Exit(2)
	}
	if err := run(*assetsRoot, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "mixed-overlay-scenario: %v\n", err)
		os.Exit(1)
	}
}

func run(assetsRoot, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	items, err := buildItems(assetsRoot)
	if err != nil {
		return err
	}
	// The typed writer builds the semantic document AND validates it through the
	// worker's decoder, so an unresolvable preset or motion fails here.
	raw, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
		PlanID:     scenarioPlanID,
		Width:      scenarioWidth,
		Height:     scenarioHeight,
		FPSNum:     scenarioFPS,
		FPSDen:     1,
		DurationMS: scenarioDuration,
		Background: &renderbatch.Surface{Kind: "color", Color: []float64{0.9333333333333333, 0.9450980392156862, 0.9058823529411765, 1}},
		Items:      items,
	})
	if err != nil {
		return fmt.Errorf("build semantic plan: %w", err)
	}
	compiled, err := renderbatch.CompileRenderPlan(raw)
	if err != nil {
		return fmt.Errorf("compile render plan: %w", err)
	}

	semanticPath := filepath.Join(outDir, "semantic_plan.json")
	renderPath := filepath.Join(outDir, "render_plan.json")
	if err := os.WriteFile(semanticPath, raw, 0o644); err != nil {
		return fmt.Errorf("write semantic plan: %w", err)
	}
	if err := os.WriteFile(renderPath, compiled, 0o644); err != nil {
		return fmt.Errorf("write render plan: %w", err)
	}

	descriptor := scenarioDescriptor{
		PlanID:       scenarioPlanID,
		Width:        scenarioWidth,
		Height:       scenarioHeight,
		FPS:          scenarioFPS,
		DurationMS:   scenarioDuration,
		SemanticPlan: semanticPath,
		RenderPlan:   renderPath,
		GapFrames:    []int{msToFrame(2200), msToFrame(5400), msToFrame(7400)},
		Items:        scenarioItems,
	}
	for i := range descriptor.Items {
		descriptor.Items[i].MidFrame = frameAt(descriptor.Items[i].StartMS, descriptor.Items[i].EndMS, midFractionFor(descriptor.Items[i]))
		descriptor.Items[i].EntryFrame = frameAt(descriptor.Items[i].StartMS, descriptor.Items[i].EndMS, entryFraction)
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scenario descriptor: %w", err)
	}
	scenarioPath := filepath.Join(outDir, "scenario.json")
	if err := os.WriteFile(scenarioPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write scenario descriptor: %w", err)
	}
	fmt.Printf("semantic plan: %s\n", semanticPath)
	fmt.Printf("render plan:   %s\n", renderPath)
	fmt.Printf("descriptor:    %s\n", scenarioPath)
	return nil
}

// midFractionFor samples a settled frame: phrases are static, portraits need
// their entrance (37 frames) finished before the presence probe reads them.
func midFractionFor(item scenarioItem) float64 {
	if item.CenterOnly {
		if item.Name == "entity_b" {
			return 0.85
		}
		return 0.75
	}
	return 0.5
}

// buildItems materializes the typed items and their content addresses. The
// portrait bytes must exist at the logical path the plan declares, so a missing
// fixture fails here instead of at render time.
func buildItems(assetsRoot string) ([]renderbatch.PlanItem, error) {
	items := make([]renderbatch.PlanItem, 0, len(scenarioItems))
	for _, spec := range scenarioItems {
		switch spec.Kind {
		case "phrase":
			items = append(items, renderbatch.PlanItem{
				ID: spec.Name, TemplateID: "IMPORTANT_PHRASE", Kind: "important_phrase",
				PresetID: spec.Preset, Text: spec.Text, StartMS: spec.StartMS, EndMS: spec.EndMS,
			})
		case "entity_image":
			sha, err := assetSHA256(filepath.Join(assetsRoot, filepath.FromSlash(spec.Asset)))
			if err != nil {
				return nil, err
			}
			id := filepath.Base(spec.Asset)
			id = id[:len(id)-len(filepath.Ext(id))]
			items = append(items, renderbatch.PlanItem{
				ID: spec.Name, TemplateID: "image_popup", Kind: "entity_image",
				PresetID: spec.Preset, EntityID: spec.EntityID,
				StartMS: spec.StartMS, EndMS: spec.EndMS,
				AssetRefs: []renderbatch.PlanAssetRef{{
					AssetID: id, SHA256: sha,
					URL:       "https://store.example/objects/" + id + filepath.Ext(spec.Asset),
					MediaType: "image/jpeg",
				}},
			})
		default:
			return nil, fmt.Errorf("unknown scenario item kind %q", spec.Kind)
		}
	}
	return items, nil
}

func assetSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("scenario asset %s is not materialized: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
