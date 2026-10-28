package overlay

// a0_bbox_census_test.go is the measurement harness for gate A0 of
// refactored/docs/operations/TICKET-PARADIGM-ALPHA-LAYER-HUD.md: a real
// bounding-box census over a corpus of semantic overlay plans
// (renderinggen.overlay-plan.v1), per item class, to decide whether the
// alpha-bbox clipping path (A2) has at least 8x pixel headroom.
//
// It is env-gated and never runs in CI or the normal suite:
//
//	PIPELINEGEN_A0_CENSUS=1        enables the census
//	PIPELINEGEN_A0_CENSUS_DIR      ':'-separated corpus dirs (recursive scan
//	                               for overlay_plan*.json and job.full.json)
//	PIPELINEGEN_A0_CENSUS_OUT      optional JSON report output path
//
// Method (conservative upper bound of the rect an alpha clip must cover):
//
//   - geometry comes from the COMPILED plan (CompileSemantic), not from raw
//     authoring fields: layer Size/Position plus the emitted AnimationTrack
//     keyframes. Motion tracks are DELTAS from the resting pose
//     (motion/types.go: position rests at 0, scale at 1), so a frame's rect
//     is center = position + delta, size = size * scale.
//   - per-frame rects are sampled on every frame of the layer window with
//     linear keyframe interpolation, then unioned over the item window.
//   - the union is inflated by (a) effects bleed (glow/blur/shadow radii),
//     (b) a 12% motion-overshoot margin (overshoot easings exceed their
//     keyframe hull), (c) a 4px anti-aliasing pad. Rotation tracks inflate
//     the rect to its circumscribed box.
//
// The result is an UPPER bound of the needed clip rect: if the upper bound
// already shows >=8x headroom, the A2 decision gate is satisfied; a lower
// real bound only improves the case.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	a0SemanticSchema     = SemanticSchema
	a0OvershootMarginPct = 12.0
	a0AAPadPx            = 4.0
	a0MaxEffectPadPx     = 256.0
	a0GateFactor         = 8.0
)

type a0ItemMeta struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	RenderKey string `json:"render_key"`
	Text      string `json:"text"`
}

type a0ItemSample struct {
	PlanID       string  `json:"plan_id"`
	ItemID       string  `json:"item_id"`
	Kind         string  `json:"kind"`
	RenderKey    string  `json:"render_key"`
	UnionW       float64 `json:"union_w"`
	UnionH       float64 `json:"union_h"`
	BaseW        float64 `json:"base_w"`
	BaseH        float64 `json:"base_h"`
	UnionPx      float64 `json:"union_px"`
	MaxFramePx   float64 `json:"max_frame_px"`
	CanvasPx     float64 `json:"canvas_px"`
	UnionFactor  float64 `json:"union_factor"`
	FrameFactor  float64 `json:"frame_factor"`
	EffectPadPx  float64 `json:"effect_pad_px"`
	AssumedPlace int     `json:"assumed_center_layers"`
}

type a0ClassStats struct {
	Class        string    `json:"class"`
	Count        int       `json:"count"`
	MedianFactor float64   `json:"median_union_factor"`
	P90Factor    float64   `json:"p90_union_factor"`
	MinFactor    float64   `json:"min_union_factor"`
	MedianFrame  float64   `json:"median_frame_factor"`
	Factors      []float64 `json:"union_factors"`
}

type a0Report struct {
	Method        string                   `json:"method"`
	CorpusDirs    []string                 `json:"corpus_dirs"`
	PlansCompiled int                      `json:"plans_compiled"`
	PlansFailed   int                      `json:"plans_failed"`
	ItemsTotal    int                      `json:"items_total"`
	ItemsDeduped  int                      `json:"items_deduped"`
	Classes       map[string]*a0ClassStats `json:"classes"`
	Overall       *a0ClassStats            `json:"overall"`
	Gate          struct {
		ThresholdFactor   float64 `json:"threshold_factor"`
		MedianUnionFactor float64 `json:"median_union_factor"`
		Pass              bool    `json:"pass"`
	} `json:"gate"`
	Samples []a0ItemSample `json:"samples"`
}

func TestA0BBoxCensus(t *testing.T) {
	if os.Getenv("PIPELINEGEN_A0_CENSUS") != "1" {
		t.Skip("A0 bbox census harness: set PIPELINEGEN_A0_CENSUS=1 with PIPELINEGEN_A0_CENSUS_DIR to run")
	}
	dirSpec := os.Getenv("PIPELINEGEN_A0_CENSUS_DIR")
	if strings.TrimSpace(dirSpec) == "" {
		t.Fatalf("PIPELINEGEN_A0_CENSUS_DIR must name the corpus directories")
	}
	dirs := filepath.SplitList(dirSpec)

	report := &a0Report{
		Method:     "compiled-plan geometry: per-frame rect sampling (linear keyframes, motion deltas from resting pose), window union, effects bleed + 12% overshoot + 4px AA inflation; conservative UPPER bound. Persisted plans: non-contract metadata stripped only where the compiler's strict decode names it (fields never carry geometry; map knob area_glow_radius_km is dropped, map items stay full-canvas and out of the A2 benefit).",
		CorpusDirs: dirs,
		Classes:    map[string]*a0ClassStats{},
		Overall:    &a0ClassStats{Class: "ALL"},
	}

	seen := map[string]struct{}{}
	for _, dir := range dirs {
		plans := a0CollectPlans(t, dir)
		t.Logf("corpus %s: %d plan document(s)", dir, len(plans))
		for _, raw := range plans {
			meta, samples, err := a0CensusPlan(raw)
			if err != nil {
				report.PlansFailed++
				t.Logf("plan compile failed: %v", err)
				continue
			}
			report.PlansCompiled++
			_ = meta
			for _, sample := range samples {
				report.ItemsTotal++
				key := sample.RenderKey
				if key == "" {
					key = sample.Kind + "|" + sample.ItemID + "|" + fmt.Sprint(sample.UnionPx)
				}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				report.ItemsDeduped++
				report.Samples = append(report.Samples, sample)
				class := report.Classes[sample.Kind]
				if class == nil {
					class = &a0ClassStats{Class: sample.Kind}
					report.Classes[sample.Kind] = class
				}
				class.Factors = append(class.Factors, sample.UnionFactor)
				class.Count++
				report.Overall.Factors = append(report.Overall.Factors, sample.UnionFactor)
				report.Overall.Count++
			}
		}
	}

	for _, class := range append([]*a0ClassStats{report.Overall}, a0ClassValues(report.Classes)...) {
		if len(class.Factors) == 0 {
			continue
		}
		sorted := append([]float64(nil), class.Factors...)
		sort.Float64s(sorted)
		class.MinFactor = sorted[0]
		class.MedianFactor = a0Percentile(sorted, 50)
		class.P90Factor = a0Percentile(sorted, 90)
	}
	report.Gate.ThresholdFactor = a0GateFactor
	report.Gate.MedianUnionFactor = report.Overall.MedianFactor
	report.Gate.Pass = report.Overall.MedianFactor >= a0GateFactor

	for _, class := range a0ClassValues(report.Classes) {
		t.Logf("class %-14s n=%-3d union-factor median=%.1fx p90=%.1fx min=%.2fx",
			class.Class, class.Count, class.MedianFactor, class.P90Factor, class.MinFactor)
	}
	t.Logf("OVERALL n=%d union-factor median=%.1fx p90=%.1fx min=%.2fx → gate(≥%.0fx)=%v",
		report.Overall.Count, report.Overall.MedianFactor, report.Overall.P90Factor,
		report.Overall.MinFactor, a0GateFactor, report.Gate.Pass)

	byFactor := append([]a0ItemSample(nil), report.Samples...)
	sort.Slice(byFactor, func(i, j int) bool { return byFactor[i].UnionFactor < byFactor[j].UnionFactor })
	for i := 0; i < len(byFactor) && i < 5; i++ {
		s := byFactor[i]
		t.Logf("widest %-14s %-38s union=%.0fx%.0f base=%.0fx%.0f factor=%.2fx",
			s.Kind, s.ItemID, s.UnionW, s.UnionH, s.BaseW, s.BaseH, s.UnionFactor)
	}

	if out := os.Getenv("PIPELINEGEN_A0_CENSUS_OUT"); strings.TrimSpace(out) != "" {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatalf("marshal report: %v", err)
		}
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatalf("write report: %v", err)
		}
		t.Logf("report written to %s", out)
	}
}

func a0ClassValues(classes map[string]*a0ClassStats) []*a0ClassStats {
	names := make([]string, 0, len(classes))
	for name := range classes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*a0ClassStats, 0, len(names))
	for _, name := range names {
		out = append(out, classes[name])
	}
	return out
}

func a0Percentile(sorted []float64, pct int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(pct)/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// a0CollectPlans finds semantic overlay plan documents below dir: standalone
// overlay_plan*.json files and embedded plans inside job.full.json snapshots.
func a0CollectPlans(t *testing.T, dir string) [][]byte {
	t.Helper()
	var plans [][]byte
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		base := filepath.Base(path)
		switch {
		case strings.HasPrefix(base, "overlay_plan") && strings.HasSuffix(base, ".json"):
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if a0IsSemanticPlan(raw) {
				plans = append(plans, raw)
			}
		case base == "job.full.json":
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			plans = append(plans, a0ExtractEmbeddedPlans(t, path, raw)...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return plans
}

func a0IsSemanticPlan(raw []byte) bool {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.SchemaVersion == a0SemanticSchema
}

// a0ExtractEmbeddedPlans recursively collects embedded overlay-plan
// documents from a job snapshot (they appear under result trees as
// "overlay_plan" objects), de-duplicated per file by their serialized bytes.
func a0ExtractEmbeddedPlans(t *testing.T, path string, raw []byte) [][]byte {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Logf("%s: not JSON: %v", path, err)
		return nil
	}
	seen := map[string]struct{}{}
	var plans [][]byte
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if sv, _ := t["schema_version"].(string); sv == a0SemanticSchema {
				if _, ok := t["items"]; ok {
					if re, err := json.Marshal(t); err == nil {
						s := string(re)
						if _, dup := seen[s]; !dup {
							seen[s] = struct{}{}
							plans = append(plans, re)
						}
					}
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(doc)
	return plans
}

// a0CompileForCensus compiles a persisted plan document. Production plans
// carry non-contract delivery metadata (drive_folder_id, map knobs, legacy
// fields) that the compiler's strict decode rejects on purpose; the census
// strips ONLY the fields the strict decoder explicitly names and retries, so
// the geometry lowering stays the compiler's own while no accepted field is
// ever dropped. Any other compile error fails as usual.
func a0CompileForCensus(raw []byte) (CompileResult, []byte, error) {
	current := raw
	for attempt := 0; attempt < 32; attempt++ {
		result, err := CompileSemantic(current)
		if err == nil {
			return result, current, nil
		}
		const marker = `unknown field "`
		idx := strings.Index(err.Error(), marker)
		if idx < 0 {
			return CompileResult{}, nil, err
		}
		rest := err.Error()[idx+len(marker):]
		end := strings.Index(rest, `"`)
		if end < 0 {
			return CompileResult{}, nil, err
		}
		field := rest[:end]
		var doc any
		if uerr := json.Unmarshal(current, &doc); uerr != nil {
			return CompileResult{}, nil, err
		}
		a0DeleteField(doc, field)
		re, merr := json.Marshal(doc)
		if merr != nil {
			return CompileResult{}, nil, err
		}
		current = re
	}
	return CompileResult{}, nil, fmt.Errorf("a0 census: too many unknown-field retries on plan")
}

func a0DeleteField(v any, field string) {
	switch t := v.(type) {
	case map[string]any:
		delete(t, field)
		for _, child := range t {
			a0DeleteField(child, field)
		}
	case []any:
		for _, child := range t {
			a0DeleteField(child, field)
		}
	}
}

// a0CensusPlan compiles one semantic plan and returns per-item bbox samples.
func a0CensusPlan(raw []byte) ([]a0ItemMeta, []a0ItemSample, error) {
	result, stripped, err := a0CompileForCensus(raw)
	if err != nil {
		return nil, nil, err
	}
	raw = stripped
	var probe struct {
		PlanID string       `json:"plan_id"`
		Items  []a0ItemMeta `json:"items"`
	}
	_ = json.Unmarshal(raw, &probe)
	kindByID := map[string]a0ItemMeta{}
	for _, item := range probe.Items {
		kindByID[item.ID] = item
	}

	canvas := result.Plan.Canvas
	canvasPx := float64(canvas.Width * canvas.Height)

	// Per-item corner hull over every layer rect of every frame.
	type corners struct {
		minX, minY, maxX, maxY float64
		started                bool
	}
	hulls := map[string]*corners{}
	maxFrameByItem := map[string]float64{}
	padByItem := map[string]float64{}
	assumedByItem := map[string]int{}
	baseByItem := map[string][2]float64{}
	for _, layer := range result.Plan.Layers {
		itemID, _, _ := strings.Cut(layer.ID, ":")
		h := hulls[itemID]
		if h == nil {
			h = &corners{}
			hulls[itemID] = h
		}
		rects, assumed, baseW, baseH := a0LayerFrameRects(layer, canvas.Width, canvas.Height)
		if base := baseByItem[itemID]; baseW*baseH > base[0]*base[1] {
			baseByItem[itemID] = [2]float64{baseW, baseH}
		}
		assumedByItem[itemID] += assumed
		if pad := a0EffectPad(layer); pad > padByItem[itemID] {
			padByItem[itemID] = pad
		}
		for _, r := range rects {
			if !h.started {
				h.minX, h.minY, h.maxX, h.maxY = r.x0, r.y0, r.x1, r.y1
				h.started = true
				continue
			}
			h.minX = math.Min(h.minX, r.x0)
			h.minY = math.Min(h.minY, r.y0)
			h.maxX = math.Max(h.maxX, r.x1)
			h.maxY = math.Max(h.maxY, r.y1)
			if px := r.area(); px > maxFrameByItem[itemID] {
				maxFrameByItem[itemID] = px
			}
		}
	}

	samples := make([]a0ItemSample, 0, len(hulls))
	for itemID, h := range hulls {
		if !h.started {
			continue
		}
		meta := kindByID[itemID]
		base := baseByItem[itemID]
		pad := padByItem[itemID] + a0AAPadPx
		w := (h.maxX - h.minX) + 2*pad
		hh := (h.maxY - h.minY) + 2*pad
		// Motion overshoot margin (overshoot easings exceed the linear hull).
		over := math.Max(w, hh) * a0OvershootMarginPct / 100
		w += 2 * over
		hh += 2 * over
		// Clamp to the canvas: a clip never exceeds the output frame.
		w = math.Min(w, float64(canvas.Width))
		hh = math.Min(hh, float64(canvas.Height))
		unionPx := w * hh
		samples = append(samples, a0ItemSample{
			PlanID: probe.PlanID, ItemID: itemID, Kind: meta.Kind, RenderKey: meta.RenderKey,
			UnionW: w, UnionH: hh, BaseW: base[0], BaseH: base[1], UnionPx: unionPx,
			MaxFramePx:   maxFrameByItem[itemID],
			CanvasPx:     canvasPx,
			UnionFactor:  canvasPx / math.Max(unionPx, 1),
			FrameFactor:  canvasPx / math.Max(maxFrameByItem[itemID], 1),
			EffectPadPx:  padByItem[itemID],
			AssumedPlace: assumedByItem[itemID],
		})
	}
	return probe.Items, samples, nil
}

type a0Rect struct{ x0, y0, x1, y1 float64 }

func (r a0Rect) area() float64 { return math.Max(0, r.x1-r.x0) * math.Max(0, r.y1-r.y0) }

// a0LayerFrameRects samples the layer's per-frame axis-aligned rect over its
// whole window. Motion tracks are deltas from the resting pose: position
// offsets translate the authored center, scale multiplies the authored size.
// It also returns the resting base size (pre-scale) for diagnostics.
func a0LayerFrameRects(layer Layer, canvasW, canvasH int) ([]a0Rect, int, float64, float64) {
	size := [2]float64{0, 0}
	if len(layer.Size) >= 2 {
		size = [2]float64{layer.Size[0], layer.Size[1]}
	} else if layer.BoxWidth > 0 && layer.BoxHeight > 0 {
		size = [2]float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}
	}
	center := [2]float64{float64(canvasW) / 2, float64(canvasH) / 2}
	assumed := 0
	if len(layer.Position) >= 2 {
		center = [2]float64{layer.Position[0], layer.Position[1]}
	} else {
		assumed = 1
	}
	if len(layer.Scale) >= 2 && layer.Scale[0] > 0 && layer.Scale[1] > 0 {
		size[0] *= layer.Scale[0]
		size[1] *= layer.Scale[1]
	}
	if size[0] <= 0 || size[1] <= 0 {
		return nil, assumed, size[0], size[1]
	}

	start := layer.StartFrame
	end := layer.StartFrame + layer.DurationFrames
	if end <= start {
		end = start + 1
	}

	trackValue := func(prop string, frame int64) (float64, bool) {
		if layer.Animation == nil {
			return 0, false
		}
		for _, track := range layer.Animation.Tracks {
			if track.Property != prop {
				continue
			}
			return a0Interpolate(track.Keyframes, frame), true
		}
		return 0, false
	}

	rects := make([]a0Rect, 0, end-start)
	for f := start; f < end; f++ {
		cx, cy := center[0], center[1]
		if v, ok := trackValue("position_x", f); ok {
			cx += v
		}
		if v, ok := trackValue("position_y", f); ok {
			cy += v
		}
		w, h := size[0], size[1]
		if v, ok := trackValue("scale_x", f); ok {
			w *= v
		} else if v, ok := trackValue("scale", f); ok {
			w *= v
		}
		if v, ok := trackValue("scale_y", f); ok {
			h *= v
		} else if v, ok := trackValue("scale", f); ok {
			h *= v
		}
		rect := a0Rect{cx - w/2, cy - h/2, cx + w/2, cy + h/2}
		if v, ok := trackValue("rotation_z", f); ok && v != 0 {
			rect = a0Circumscribed(rect, v)
		}
		rects = append(rects, rect)
	}
	return rects, assumed, size[0], size[1]
}

// a0Interpolate linearly interpolates (or holds) keyframe values at frame.
func a0Interpolate(keyframes []AnimationKeyframe, frame int64) float64 {
	if len(keyframes) == 0 {
		return 0
	}
	first := a0Numeric(keyframes[0].Value)
	last := a0Numeric(keyframes[len(keyframes)-1].Value)
	if frame <= keyframes[0].Frame {
		return first
	}
	if frame >= keyframes[len(keyframes)-1].Frame {
		return last
	}
	for i := 1; i < len(keyframes); i++ {
		if frame <= keyframes[i].Frame {
			lo, hi := keyframes[i-1], keyframes[i]
			span := float64(hi.Frame - lo.Frame)
			if span <= 0 {
				return a0Numeric(hi.Value)
			}
			t := float64(frame-lo.Frame) / span
			return a0Numeric(lo.Value)*(1-t) + a0Numeric(hi.Value)*t
		}
	}
	return last
}

func a0Numeric(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

// a0Circumscribed inflates a rect rotated by deg around its center to the
// axis-aligned box that contains the rotated corners.
func a0Circumscribed(r a0Rect, deg float64) a0Rect {
	rad := deg * math.Pi / 180
	cx, cy := (r.x0+r.x1)/2, (r.y0+r.y1)/2
	hw, hh := (r.x1-r.x0)/2, (r.y1-r.y0)/2
	c, s := math.Abs(math.Cos(rad)), math.Abs(math.Sin(rad))
	nw := hw*c + hh*s
	nh := hw*s + hh*c
	return a0Rect{cx - nw, cy - nh, cx + nw, cy + nh}
}

// a0EffectPad is the effects bleed a clip rect must cover around the layer
// geometry: the largest radius-like parameter over declared effects and
// effect-parameter tracks (glow/blur/shadow reach outside the geometry).
func a0EffectPad(layer Layer) float64 {
	pad := 0.0
	consider := func(params map[string]any) {
		for _, key := range []string{"radius", "blur_radius", "distance", "spread", "sigma"} {
			if v, ok := params[key]; ok {
				if n := a0Numeric(v); n > pad {
					pad = n
				}
			}
		}
	}
	for _, effect := range layer.Effects {
		consider(effect.Params)
	}
	for _, track := range layer.EffectParamTracks {
		for _, keyframe := range track.Keyframes {
			if n := math.Abs(a0Numeric(keyframe.Value)); n > pad {
				pad = n
			}
		}
	}
	return math.Min(pad*2, a0MaxEffectPadPx)
}
