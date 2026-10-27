package overlay

import (
	"encoding/json"
	"testing"
)

// compile_path_bench_test.go measures the semantic compile path end to end —
// the "phase metrics" that gate compile-path micro-optimizations. The rule the
// worker follows (docs/tickets/TICKET-PERF-REVOLUTIONARY-IDEAS.md §4) is that
// no compile-path rewrite is justified without a repeatable number first; this
// file IS that number.
//
// It exists to answer one question with data: does the compile path spend
// "tens of ms" per plan (which would justify rewriting the remaining
// json.Marshal→Unmarshal round-trips, e.g. decodeChainSegments, and typing the
// resolvedItem.Params map[string]any surface)? The recorded verdict below says
// no — the numbers live far below that threshold, so those rewrites stay OFF
// the table until a real job profile says otherwise.
//
// Verdict (2026-10-27, go test -bench on the reference machine):
//
//	BenchmarkCompileSemantic-24         1377    1831545 ns/op
//	BenchmarkCompileSemanticChain-24    1304    1871813 ns/op
//
// ~1.8 ms per plan: two orders of magnitude below the "tens of ms"
// threshold and noise next to render/encode (seconds) and asset
// materialization (tens of ms+). Do NOT treat the round-trips as free (they
// allocate); treat them as not-yet-worth-rewriting, which is exactly what
// the conditional promised.

// compileBenchDocument builds a realistic editorial overlay plan: a burned
// subtitle track, a background and the item mix the worker sees in production
// (phrases + metric/date cards), with animation policies active. The fixture
// is intentionally asset-free so the benchmark measures the COMPILE path and
// not asset validation.
func compileBenchDocument(items []any) map[string]any {
	return map[string]any{
		"schema_version": SemanticSchema,
		"plan_id":        "compile-bench",
		"video_id":       "compile-bench",
		"width":          1920,
		"height":         1080,
		"fps_num":        30,
		"fps_den":        1,
		"language":       "it",
		"duration_ms":    8000,
		"background":     map[string]any{"kind": "color", "color": []any{0.0, 0.0, 0.0, 1.0}},
		"subtitles": map[string]any{
			"asset_refs": []any{map[string]any{
				"asset_id": "ass", "sha256": sixtyFourZeros(),
				"url": "https://cdn.example/subs.ass", "media_type": "text/x-ssa"}},
			"mode":  "burn",
			"style": map[string]any{"position": "bottom_center", "font_size_px": 52.0, "color": "#FFFFFF"},
		},
		"items": items,
	}
}

// compileBenchItems is the production-shape item mix: three animated phrases
// and two metric cards spread over the timeline.
func compileBenchItems() []any {
	return []any{
		map[string]any{"id": "p1", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": "phrase_default", "text": "Prima frase importante",
			"start_ms": 0, "end_ms": 2500},
		map[string]any{"id": "m1", "kind": "number", "template_id": "METRIC_STAT_CARD",
			"preset_id": "phrase_default", "text": "59.99",
			"start_ms": 2500, "end_ms": 4500},
		map[string]any{"id": "p2", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": "phrase_default", "text": "Seconda frase importante",
			"start_ms": 4500, "end_ms": 6000},
		map[string]any{"id": "m2", "kind": "number", "template_id": "TIMELINE_DATE_CARD",
			"preset_id": "phrase_default", "text": "March 5, 2026",
			"start_ms": 6000, "end_ms": 7000},
		map[string]any{"id": "p3", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": "phrase_default", "text": "Terza frase importante",
			"start_ms": 7000, "end_ms": 8000},
	}
}

// compileBenchChainItems is the chain shape: one phrase_chain item whose five
// segments exercise decodeChainSegments (the last json.Marshal→Unmarshal
// round-trip left on the per-item compile path).
func compileBenchChainItems() []any {
	segments := []any{
		map[string]any{"text": "SEG ONE", "motion_id": "phrase_apple_clean_07_slide_up_soft",
			"preset_id": "phrase_default", "template_id": "IMPORTANT_PHRASE",
			"kind": "text_phrase", "start_us": 0, "duration_us": 1600000},
		map[string]any{"text": "SEG TWO", "motion_id": "soft_edge_spotlight_dissolve",
			"preset_id": "phrase_default", "template_id": "IMPORTANT_PHRASE",
			"kind": "text_phrase", "start_us": 1600000, "duration_us": 1600000},
		map[string]any{"text": "SEG THREE", "motion_id": "phrase_apple_clean_07_slide_up_soft",
			"preset_id": "phrase_default", "template_id": "IMPORTANT_PHRASE",
			"kind": "text_phrase", "start_us": 3200000, "duration_us": 1600000},
		map[string]any{"text": "SEG FOUR", "motion_id": "soft_edge_spotlight_dissolve",
			"preset_id": "phrase_default", "template_id": "IMPORTANT_PHRASE",
			"kind": "text_phrase", "start_us": 4800000, "duration_us": 1600000},
		map[string]any{"text": "SEG FIVE", "motion_id": "phrase_apple_clean_07_slide_up_soft",
			"preset_id": "phrase_default", "template_id": "IMPORTANT_PHRASE",
			"kind": "text_phrase", "start_us": 6400000, "duration_us": 1600000},
	}
	return []any{
		map[string]any{"id": "chain-bench", "kind": "phrase_chain", "template_id": "PHRASE_CHAIN",
			"preset_id": "phrase_default", "motion_id": "phrase_apple_clean_07_slide_up_soft",
			"text": "FIRST", "start_ms": 0, "end_ms": 8000,
			"params": map[string]any{"chain_camera": "fly_under_fade", "chain_count": 5,
				"chain_segments": segments}},
	}
}

func benchmarkCompileSemantic(b *testing.B, document map[string]any) {
	raw, err := json.Marshal(document)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CompileSemantic(raw); err != nil {
			b.Fatalf("CompileSemantic: %v", err)
		}
	}
}

// BenchmarkCompileSemantic is the production-shape plan (5 items + burned
// subtitles + background). Run with:
//
//	go test -run '^$' -bench 'BenchmarkCompileSemantic' ./internal/overlay/
//
// Verdict (2026-10-27, reference machine, 24 threads): 1831545 ns/op for the
// production shape — far below the "tens of ms" threshold — so the conditional
// compile-path rewrites (typing resolvedItem.Params, removing the
// decodeChainSegments json.Marshal→Unmarshal round-trip) stay
// measured-but-unjustified. Re-run this benchmark when a real job profile
// claims compile time matters; if it ever crosses tens of ms per plan, do the
// rewrites (they are known, localized and test-covered), do not re-litigate
// them here.
func BenchmarkCompileSemantic(b *testing.B) {
	benchmarkCompileSemantic(b, compileBenchDocument(compileBenchItems()))
}

// BenchmarkCompileSemanticChain isolates the chain-item path (5 segments):
// decodeChainSegments' json.Marshal→Unmarshal round-trip lives here, one
// round-trip per chain ITEM (not per segment).
func BenchmarkCompileSemanticChain(b *testing.B) {
	benchmarkCompileSemantic(b, compileBenchDocument(compileBenchChainItems()))
}
