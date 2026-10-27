package overlay

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// compile_single_decode_test.go pins the single-decode prepare-path contract:
//
//   - CompileSemantic decodes the raw semantic document exactly once and
//     carries the subtitle contract on its result (Subtitle /
//     ResolveSubtitleStyle);
//   - the standalone SubtitleAsset / SubtitleStyleAsset entries stay behavior-
//     identical to that result (they share one resolution core, so the two
//     views can never drift);
//   - the historical CompileSemantic error taxonomy (invalid JSON, unsupported
//     schema, strict-decode failure, including the schema-before-strict
//     precedence) is preserved even though the strict decode now runs first.

// subtitleCompilePlanJSON builds a compile-valid semantic plan (background +
// duration, so the compiler's fail-closed primitives check passes) carrying a
// subtitles block with the given mode and optional style map.
func subtitleCompilePlanJSON(mode, style string) string {
	subs := `{"asset_refs":[{"asset_id":"ass","sha256":"` + sixtyFourZeros() + `","url":"https://cdn.example/subs.ass","media_type":"text/x-ssa"}],"mode":` + mode
	if style != "" {
		subs += `,"style":` + style
	}
	subs += `}`
	return `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"sub","video_id":"v",` +
		`"width":1920,"height":1080,"fps_num":30,"fps_den":1,"language":"it",` +
		`"duration_ms":3000,"background":{"kind":"color","color":[0,0,0,1]},` +
		`"subtitles":` + subs + `,"items":[]}`
}

// TestCompileResultSubtitleContractMatchesSubtitleAsset locks equality between
// the compile-result subtitle contract and the standalone SubtitleAsset for
// every declaration shape the burn gate distinguishes: burn mode, non-burn
// mode, and no subtitles at all.
func TestCompileResultSubtitleContractMatchesSubtitleAsset(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantPresent bool
		wantBurn    bool
	}{
		{"burn", subtitleCompilePlanJSON(`"burn"`, `{"position":"bottom_center","font_size_px":52,"color":"#FFFFFF"}`), true, true},
		{"burn_case_and_space", subtitleCompilePlanJSON(`" BURN "`, `{"position":"bottom_center","font_size_px":52,"color":"#FFFFFF"}`), true, true},
		{"non_burn", subtitleCompilePlanJSON(`"watch"`, ""), true, false},
		{"no_subtitles", backgroundOnlyPlanJSON(`{"kind":"color","color":[0,0,0,1]}`), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("CompileSemantic: %v", err)
			}
			hash, burn, ok, err := SubtitleAsset(raw)
			if err != nil {
				t.Fatalf("SubtitleAsset: %v", err)
			}
			if result.Subtitle.Present != ok || result.Subtitle.Burn != burn || result.Subtitle.Hash != hash {
				t.Fatalf("subtitle contract drift: result=%+v standalone=(hash=%q burn=%v ok=%v)",
					result.Subtitle, hash, burn, ok)
			}
			if ok != tc.wantPresent || burn != tc.wantBurn {
				t.Fatalf("contract = (present=%v burn=%v), want (present=%v burn=%v)",
					ok, burn, tc.wantPresent, tc.wantBurn)
			}
			if ok && hash != sixtyFourZeros() {
				t.Fatalf("subtitle hash = %q, want the declared lowercase sha256", hash)
			}
		})
	}
}

// TestCompileResultResolveSubtitleStyleMatchesSubtitleStyleAsset locks equality
// between the compile-result style resolution and the standalone
// SubtitleStyleAsset: same style, same safe-area box, no second decode needed
// on the processor's prepare path.
func TestCompileResultResolveSubtitleStyleMatchesSubtitleStyleAsset(t *testing.T) {
	for _, position := range []string{"bottom_center", "top_center", "middle_center"} {
		t.Run(position, func(t *testing.T) {
			raw := []byte(subtitleCompilePlanJSON(`"burn"`,
				`{"position":"`+position+`","font_size_px":52,"color":"#FFFFFF"}`))
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("CompileSemantic: %v", err)
			}
			gotStyle, gotBox, err := result.ResolveSubtitleStyle()
			if err != nil {
				t.Fatalf("ResolveSubtitleStyle: %v", err)
			}
			wantStyle, wantBox, err := SubtitleStyleAsset(raw)
			if err != nil {
				t.Fatalf("SubtitleStyleAsset: %v", err)
			}
			if gotStyle == nil || wantStyle == nil {
				t.Fatalf("style must resolve on both paths: got=%v want=%v", gotStyle, wantStyle)
			}
			gotStyleJSON, _ := json.Marshal(gotStyle)
			wantStyleJSON, _ := json.Marshal(wantStyle)
			if string(gotStyleJSON) != string(wantStyleJSON) {
				t.Fatalf("style drift:\n result=%s\n standalone=%s", gotStyleJSON, wantStyleJSON)
			}
			if !reflect.DeepEqual(gotBox, wantBox) {
				t.Fatalf("box drift: result=%+v standalone=%+v", gotBox, wantBox)
			}
		})
	}
}

// TestCompileSemanticErrorTaxonomy preserves the historical observable errors
// of CompileSemantic even though the strict decode now runs first: invalid
// JSON reports as "decode plan", an unsupported schema wins over any
// strict-decode complaint (the probe ran first), and a strict-decode failure on
// a schema-valid document reports as "decode semantic plan".
func TestCompileSemanticErrorTaxonomy(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantPart string
	}{
		{"invalid_json", `{"schema_version":`, "overlay: decode plan:"},
		{"wrong_schema", `{"schema_version":"other.v9","plan_id":"p","video_id":"v","width":1,"height":1,"fps_num":1,"fps_den":1,"items":[]}`, "unsupported plan schema"},
		{"unknown_field", `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1,"height":1,"fps_num":1,"fps_den":1,"duration_ms":1,"background":{"kind":"color","color":[0,0,0,1]},"items":[],"nope":1}`, "overlay: decode semantic plan:"},
		// Historical precedence: the schema probe rejected the document before
		// the strict decoder could complain about the unknown field.
		{"wrong_schema_and_unknown_field", `{"schema_version":"other.v9","nope":1}`, "unsupported plan schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileSemantic([]byte(tc.raw))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantPart) {
				t.Fatalf("error %q does not contain %q", err, tc.wantPart)
			}
		})
	}
}

// TestCompileSemanticDefersSubtitleStyleResolution pins the deferred-style
// contract: a plan whose subtitles are NOT burned never resolves a style at
// compile time (the historical call site resolved only for burn plans), so a
// malformed style block must not fail the compile — while the resolution entry
// points still reject it fail-closed when asked.
func TestCompileSemanticDefersSubtitleStyleResolution(t *testing.T) {
	raw := []byte(subtitleCompilePlanJSON(`"watch"`, `{"position":"bottom_center","font_size_px":"huge"}`))
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic must not resolve a non-burn style: %v", err)
	}
	if !result.Subtitle.Present || result.Subtitle.Burn {
		t.Fatalf("subtitle contract = %+v, want present non-burn", result.Subtitle)
	}
	if style, _, err := result.ResolveSubtitleStyle(); err == nil {
		t.Fatalf("ResolveSubtitleStyle must reject the malformed style block, got %+v", style)
	}
	if style, _, err := SubtitleStyleAsset(raw); err == nil {
		t.Fatalf("SubtitleStyleAsset must reject the malformed style block, got %+v", style)
	}
}
