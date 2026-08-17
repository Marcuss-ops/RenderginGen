// Package overlay owns the RenderingGen-side bridge from the semantic overlay
// contract emitted by PipelineGen to the concrete plan consumed by Chronon.
package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
)

const SemanticSchema = "renderinggen.overlay-plan.v1"

// CanonicalFontHash is the SHA-256 of the vendored deterministic font
// fixture (testdata/golden/DejaVuSans.ttf) every text layer carries. It is
// projected into the compiled job's assets so materialization resolves it.
const CanonicalFontHash = "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648"

type semanticPlan struct {
	SchemaVersion   string         `json:"schema_version"`
	PlanID          string         `json:"plan_id"`
	VideoID         string         `json:"video_id"`
	Width           int            `json:"width"`
	Height          int            `json:"height"`
	FPS             int            `json:"fps"`
	OutputProfileID string         `json:"output_profile_id,omitempty"`
	Renderer        string         `json:"renderer_version,omitempty"`
	Items           []semanticItem `json:"items"`
}

type semanticItem struct {
	ID         string          `json:"id"`
	TemplateID string          `json:"template_id"`
	Text       string          `json:"text,omitempty"`
	StartMS    int64           `json:"start_ms"`
	EndMS      int64           `json:"end_ms"`
	Params     map[string]any  `json:"params,omitempty"`
	Assets     []semanticAsset `json:"asset_refs,omitempty"`
}

type semanticAsset struct {
	AssetID   string `json:"asset_id"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type,omitempty"`
}

type Plan struct {
	Schema  string  `json:"schema"`
	Version int     `json:"version"`
	JobID   string  `json:"job_id"`
	Canvas  Canvas  `json:"canvas"`
	Layers  []Layer `json:"layers"`
	Output  Output  `json:"output"`
}

type Canvas struct {
	Width          int   `json:"width"`
	Height         int   `json:"height"`
	FPS            int   `json:"fps"`
	DurationFrames int64 `json:"duration_frames"`
}

type Layer struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Asset string `json:"asset,omitempty"`
	// Source is the video-source logical path for Video layers
	// (VIDEO_BACKGROUND): Chronon video layers reference `source`.
	Source         string    `json:"source,omitempty"`
	Text           string    `json:"text,omitempty"`
	Preset         string    `json:"preset,omitempty"`
	Font           string    `json:"font,omitempty"`
	FontSize       float64   `json:"font_size,omitempty"`
	BoxWidth       int       `json:"box_width,omitempty"`
	BoxHeight      int       `json:"box_height,omitempty"`
	Fit            string    `json:"fit,omitempty"`
	Position       []float64 `json:"position,omitempty"`
	Color          []float64 `json:"color,omitempty"`
	Opacity        float64   `json:"opacity,omitempty"`
	BlendMode      string    `json:"blend_mode,omitempty"`
	Loop           bool      `json:"loop,omitempty"`
	StartFrame     int64     `json:"start_frame"`
	DurationFrames int64     `json:"duration_frames"`
	// Animation is the motion preset applied to the layer, projected from
	// Params["animation"]["preset"] (e.g. fade_in, scale_drop).
	Animation *LayerAnimation `json:"animation,omitempty"`
}

// LayerAnimation mirrors the chronon.render-plan.v1 layer animation block.
type LayerAnimation struct {
	Preset string `json:"preset"`
}

// layoutCandidate mirrors the PipelineGen slot engine for the semantic
// bridge: string positions resolve to canvas slots, collisions are separated
// by priority. The two implementations are deterministic mirrors (like the
// golden twins) — same table, same order, same result.
type layoutCandidate struct {
	index      int
	slot       string
	boxW       int
	boxH       int
	priority   float64
	startFrame int64
	endFrame   int64
	canvasW    int
	canvasH    int
}

type canvasSlot struct {
	name string
	x    func(w, h, bw, bh int) float64
	y    func(w, h, bw, bh int) float64
}

func slotCenteredX(w, h, bw, bh int) float64 { return float64(w-bw) / 2 }
func slotCenteredY(w, h, bw, bh int) float64 { return float64(h-bh) / 2 }

var semanticSlots = []canvasSlot{
	{name: "center", x: slotCenteredX, y: slotCenteredY},
	{name: "top", x: slotCenteredX, y: func(w, h, bw, bh int) float64 { return safeMargin }},
	{name: "right", x: func(w, h, bw, bh int) float64 { return float64(w) - safeMargin - float64(bw) }, y: slotCenteredY},
	{name: "corner", x: func(w, h, bw, bh int) float64 { return float64(w) - safeMargin - float64(bw) }, y: func(w, h, bw, bh int) float64 { return safeMargin }},
	{name: "bottom", x: slotCenteredX, y: func(w, h, bw, bh int) float64 { return float64(h) - safeMargin - float64(bh) }},
	{name: "left", x: func(w, h, bw, bh int) float64 { return safeMargin }, y: slotCenteredY},
	{name: "right_bottom", x: func(w, h, bw, bh int) float64 { return float64(w) - safeMargin - float64(bw) }, y: func(w, h, bw, bh int) float64 { return float64(h) - safeMargin - float64(bh) }},
	{name: "left_bottom", x: func(w, h, bw, bh int) float64 { return safeMargin }, y: func(w, h, bw, bh int) float64 { return float64(h) - safeMargin - float64(bh) }},
}

const safeMargin = 48.0

var semanticFallbackOrder = []string{"right", "corner", "right_bottom", "left", "left_bottom", "top", "bottom", "center"}

func semanticSlotFor(position string) string {
	switch strings.ToLower(strings.TrimSpace(position)) {
	case "center":
		return "center"
	case "top":
		return "top"
	case "corner":
		return "corner"
	case "bottom", "lower":
		return "bottom"
	case "left":
		return "left"
	default:
		return "right"
	}
}

// layoutImages assigns canvas positions to image candidates, mirroring the
// PipelineGen layout engine: priority desc, then plan order; semantic slot
// first, then the first free fallback slot over the layer's frame range.
func layoutImages(layers []Layer, candidates []layoutCandidate) {
	if len(candidates) == 0 {
		return
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].index < candidates[j].index
	})
	type occupancy struct {
		start, end int64
	}
	occupied := map[string]occupancy{}
	free := func(name string, start, end int64) bool {
		occ, taken := occupied[name]
		if !taken {
			return true
		}
		return end <= occ.start || start >= occ.end
	}
	slotRect := func(name string, w, h, bw, bh int) (float64, float64) {
		for _, slot := range semanticSlots {
			if slot.name == name {
				return slot.x(w, h, bw, bh), slot.y(w, h, bw, bh)
			}
		}
		return float64(w-bw) / 2, float64(h-bh) / 2
	}
	for _, candidate := range candidates {
		chosen := candidate.slot
		if !free(candidate.slot, candidate.startFrame, candidate.endFrame) {
			for _, fallback := range semanticFallbackOrder {
				if fallback == candidate.slot {
					continue
				}
				if free(fallback, candidate.startFrame, candidate.endFrame) {
					chosen = fallback
					break
				}
			}
		}
		x, y := slotRect(chosen, candidate.canvasW, candidate.canvasH, candidate.boxW, candidate.boxH)
		layers[candidate.index].Position = []float64{x, y}
		occupied[chosen] = occupancy{start: candidate.startFrame, end: candidate.endFrame}
	}
}

type Output struct {
	Path      string `json:"path"`
	Format    string `json:"format"`
	Codec     string `json:"codec"`
	ProfileID string `json:"profile_id,omitempty"`
}

type Asset struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
}

// CompileIfSemantic returns the input unchanged for an already-concrete
// Chronon plan. For a PipelineGen semantic plan it returns the concrete plan
// and the content-addressed assets that must be materialized first.
func CompileIfSemantic(raw []byte) ([]byte, []Asset, bool, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, false, fmt.Errorf("overlay: decode plan: %w", err)
	}
	if probe.SchemaVersion == "" {
		return raw, nil, false, nil
	}
	if probe.SchemaVersion != SemanticSchema {
		return nil, nil, true, fmt.Errorf("overlay: unsupported semantic schema %q", probe.SchemaVersion)
	}
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, nil, true, fmt.Errorf("overlay: decode semantic plan: %w", err)
	}
	if err := validate(src); err != nil {
		return nil, nil, true, err
	}
	frameAt := func(ms int64) int64 { return int64(math.Round(float64(ms) * float64(src.FPS) / 1000)) }
	var maxEnd int64
	for _, item := range src.Items {
		if item.EndMS > maxEnd {
			maxEnd = item.EndMS
		}
	}
	plan := Plan{
		Schema: "chronon.render-plan", Version: 1, JobID: src.PlanID,
		Canvas: Canvas{Width: src.Width, Height: src.Height, FPS: src.FPS, DurationFrames: frameAt(maxEnd)},
		Output: Output{Path: "result.mp4", Format: "mp4", Codec: "h264", ProfileID: src.OutputProfileID},
	}
	var assets []Asset
	seenAssets := map[string]bool{}
	var layoutCandidates []layoutCandidate
	needsFont := false
	contrastVeilAdded := false
	for i, item := range src.Items {
		layer, candidate, err := compileLayer(src, item, frameAt)
		if err != nil {
			return nil, nil, true, err
		}
		if candidate != nil {
			candidate.index = i
			layoutCandidates = append(layoutCandidates, *candidate)
		}
		if layer.Type == "text" {
			needsFont = true
		}
		plan.Layers = append(plan.Layers, layer)
		// The render-plan facade's color primitive is canvas-sized in the
		// Chronon runtime. Add one veil immediately after the background rather
		// than pretending it is a rounded card; this keeps the output honest and
		// makes white text readable on all six supplied light backgrounds.
		if !contrastVeilAdded && (item.TemplateID == "BACKGROUND" || item.TemplateID == "VIDEO_BACKGROUND") {
			contrastVeilAdded = true
			plan.Layers = append(plan.Layers, Layer{
				ID:             "contrast-veil",
				Type:           "color",
				Color:          []float64{0.01, 0.015, 0.03, 1},
				Opacity:        0.70,
				StartFrame:     0,
				DurationFrames: frameAt(maxEnd),
			})
		}
		for _, ref := range item.Assets {
			if ref.SHA256 == "" || seenAssets[ref.SHA256] {
				continue
			}
			seenAssets[ref.SHA256] = true
			assets = append(assets, Asset{Hash: ref.SHA256, LogicalPath: assetPath(ref.URL)})
		}
	}
	// Resolve semantic image slots with collision avoidance (mirror of the
	// PipelineGen layout engine; explicit numeric positions untouched).
	layoutImages(plan.Layers, layoutCandidates)
	// Text layers must carry the canonical vendored font as a queue asset;
	// without it materialization fails (the golden canary caught this).
	if needsFont && !seenAssets[CanonicalFontHash] {
		seenAssets[CanonicalFontHash] = true
		assets = append(assets, Asset{Hash: CanonicalFontHash, LogicalPath: "assets/fonts/DejaVuSans.ttf"})
	}
	b, err := json.Marshal(plan)
	if err != nil {
		return nil, nil, true, fmt.Errorf("overlay: encode Chronon plan: %w", err)
	}
	return b, assets, true, nil
}

func validate(p semanticPlan) error {
	if p.PlanID == "" || p.VideoID == "" || p.Width <= 0 || p.Height <= 0 || p.FPS <= 0 {
		return fmt.Errorf("overlay: semantic plan identity and canvas are required")
	}
	if p.Renderer != "" && p.Renderer != "chronon" {
		return fmt.Errorf("overlay: unsupported renderer %q", p.Renderer)
	}
	// Templates that render an asset source; an item of these kinds without a
	// resolvable asset must be rejected before rendering, never silently fixed.
	assetTemplates := map[string]bool{
		"IMAGE_OVERLAY":    true,
		"PRODUCT":          true,
		"LOGO":             true,
		"BACKGROUND":       true,
		"VIDEO_BACKGROUND": true,
	}
	seen := map[string]bool{}
	for _, item := range p.Items {
		if item.ID == "" || item.TemplateID == "" || seen[item.ID] {
			return fmt.Errorf("overlay: invalid or duplicate item %q", item.ID)
		}
		seen[item.ID] = true
		if _, ok := semanticTemplateRegistry[item.TemplateID]; !ok {
			return fmt.Errorf("overlay: unsupported template %q", item.TemplateID)
		}
		if item.StartMS < 0 || item.EndMS <= item.StartMS {
			return fmt.Errorf("overlay: invalid timing for item %q", item.ID)
		}
		if assetTemplates[item.TemplateID] && len(item.Assets) == 0 {
			return fmt.Errorf("overlay: item %q (%s) requires an asset source", item.ID, item.TemplateID)
		}
		for _, ref := range item.Assets {
			if ref.AssetID == "" {
				return fmt.Errorf("overlay: item %q has asset without asset_id", item.ID)
			}
			if ref.SHA256 == "" {
				return fmt.Errorf("overlay: item %q has asset %q without sha256", item.ID, ref.AssetID)
			}
		}
	}
	if len(p.Items) == 0 {
		return fmt.Errorf("overlay: semantic plan has no items")
	}
	return nil
}

// templateShape maps a semantic template to its concrete layer shape. The
// values mirror PipelineGen's template registry so both sides compile the
// same document for the same plan.
type templateShape struct {
	Type            string
	Preset          string
	AnimationPreset string
	Fit             string
	BoxWidth        int
	BoxHeight       int
	Position        []float64
	Card            bool
	CardWidth       int
	CardHeight      int
	CardPosition    []float64
	CardColor       []float64
	CardOpacity     float64
}

var semanticTemplateRegistry = map[string]templateShape{
	// These presets are part of Chronon's built-in content registry.  Keep the
	// mapping here (rather than depending on Chronon source at runtime) so the
	// wire contract remains stable while RenderingGen can use the richer
	// showcase treatments: cards, contrast, and motion.
	"IMPORTANT_PHRASE": {Type: "text", Preset: "caption_safe_area", AnimationPreset: "fade_in", Card: true, CardWidth: 1080, CardHeight: 180, CardPosition: []float64{0, 0}, CardColor: []float64{0.02, 0.03, 0.08, 1}, CardOpacity: 0.86},
	"IMPORTANT_WORD":   {Type: "text", Preset: "kinetic_word", AnimationPreset: "soft_pop", Card: true, CardWidth: 520, CardHeight: 150, CardPosition: []float64{0, 0}, CardColor: []float64{0.02, 0.03, 0.08, 1}, CardOpacity: 0.90},
	"NUMBER":           {Type: "text", Preset: "kinetic_word", AnimationPreset: "soft_pop"},
	"QUOTE":            {Type: "text", Preset: "caption_safe_area", AnimationPreset: "fade_in", Card: true, CardWidth: 1080, CardHeight: 180, CardPosition: []float64{0, 0}, CardColor: []float64{0.02, 0.03, 0.08, 1}, CardOpacity: 0.86},
	// The current runtime image does not expose lower-third placement through
	// the generic plan facade reliably. Centered safe-area text is a deliberate
	// fail-safe until that runtime contract grows a positional text anchor.
	"PERSON":           {Type: "text", Preset: "title_centered", AnimationPreset: "fade_in", Card: true, CardWidth: 720, CardHeight: 130, CardPosition: []float64{0, -220}, CardColor: []float64{0.02, 0.03, 0.08, 1}, CardOpacity: 0.88},
	"ORGANIZATION":     {Type: "text", Preset: "title_centered", AnimationPreset: "fade_in", Card: true, CardWidth: 720, CardHeight: 130, CardPosition: []float64{0, -220}, CardColor: []float64{0.02, 0.03, 0.08, 1}, CardOpacity: 0.88},
	"LOCATION":         {Type: "text", Preset: "title_centered", AnimationPreset: "fade_in", Card: true, CardWidth: 720, CardHeight: 130, CardPosition: []float64{0, -220}, CardColor: []float64{0.02, 0.03, 0.08, 1}, CardOpacity: 0.88},
	"CONCEPT":          {Type: "text", Preset: "caption_safe_area", AnimationPreset: "fade_in", Card: true, CardWidth: 900, CardHeight: 150, CardPosition: []float64{0, 0}, CardColor: []float64{0.02, 0.03, 0.08, 1}, CardOpacity: 0.86},
	"IMAGE_OVERLAY":    {Type: "image", Fit: "contain", BoxWidth: 260, BoxHeight: 260, Position: []float64{380, 0}},
	"PRODUCT":          {Type: "image", Fit: "contain", BoxWidth: 420, BoxHeight: 420, Position: []float64{380, 0}},
	"LOGO":             {Type: "image", Fit: "contain", BoxWidth: 180, BoxHeight: 180, Position: []float64{1060, 500}},
	"BACKGROUND":       {Type: "image", Fit: "cover"},
	"VIDEO_BACKGROUND": {Type: "video", Fit: "cover"},
}

// compileLayer projects ONE semantic item onto a concrete Chronon layer,
// honoring item Params (position/box/preset/animation/font_size/priority).
// The returned layer plus (candidate, bool) reports whether the item is an
// auto-laid-out image (string slot position) for the collision pass.
func compileLayer(p semanticPlan, item semanticItem, frameAt func(int64) int64) (Layer, *layoutCandidate, error) {
	start, end := frameAt(item.StartMS), frameAt(item.EndMS)
	if end <= start {
		return Layer{}, nil, fmt.Errorf("overlay: item %q rounds to empty frame range", item.ID)
	}
	shape, ok := semanticTemplateRegistry[item.TemplateID]
	if !ok {
		return Layer{}, nil, fmt.Errorf("overlay: unsupported template %q", item.TemplateID)
	}
	layer := Layer{
		ID: item.ID, Type: shape.Type, Preset: shape.Preset, Fit: shape.Fit,
		BoxWidth: shape.BoxWidth, BoxHeight: shape.BoxHeight, Position: shape.Position,
		StartFrame: start, DurationFrames: end - start,
	}
	if item.TemplateID == "BACKGROUND" || item.TemplateID == "VIDEO_BACKGROUND" {
		layer.BoxWidth, layer.BoxHeight = p.Width, p.Height
		layer.StartFrame, layer.DurationFrames = 0, frameAt(maxEnd(p.Items))
	}
	if item.Text != "" {
		layer.Text = item.Text
	}
	// Text layers must carry a font (the runtime bundles none); the
	// canonical vendored font is projected into the assets below.
	if shape.Type == "text" {
		layer.Font = "assets/fonts/DejaVuSans.ttf"
		// Auto-fit mirror: display text beyond the character budget gets a
		// deterministic font_size override (same buckets as PipelineGen's
		// layout engine) so presets never clip ink.
		if size, ok := fitFontSize(item.Text); ok {
			layer.FontSize = size
		}
	}
	// Per-item params override template defaults.
	if v := paramString(item.Params, "preset"); v != "" {
		layer.Preset = v
	}
	if v := paramString(item.Params, "fit"); v != "" {
		layer.Fit = v
	}
	if v := paramInt(item.Params, "box_width"); v > 0 {
		layer.BoxWidth = v
	}
	if v := paramInt(item.Params, "box_height"); v > 0 {
		layer.BoxHeight = v
	}
	if v := paramFloat(item.Params, "font_size"); v > 0 {
		layer.FontSize = v
	}
	if preset := paramAnimation(item.Params); preset != "" {
		layer.Animation = &LayerAnimation{Preset: preset}
	} else if shape.AnimationPreset != "" {
		layer.Animation = &LayerAnimation{Preset: shape.AnimationPreset}
	}
	var candidate *layoutCandidate
	if numPos, numeric := paramPosition(item.Params, "position"); numeric {
		layer.Position = numPos
	} else if pos := paramString(item.Params, "position"); pos != "" && shape.Type == "image" {
		// Semantic slot: defer to the collision-avoiding layout pass.
		layer.Position = nil
		candidate = &layoutCandidate{
			index: -1, slot: semanticSlotFor(pos),
			boxW: layer.BoxWidth, boxH: layer.BoxHeight,
			priority:   paramFloat(item.Params, "priority"),
			startFrame: start, endFrame: end,
			canvasW: p.Width, canvasH: p.Height,
		}
	}
	if len(item.Assets) > 0 {
		logical := assetPath(item.Assets[0].URL)
		if layer.Type == "video" {
			layer.Source = logical
		} else {
			layer.Asset = logical
		}
	}
	return layer, candidate, nil
}

// fitTextBudget is the display rune budget before auto-fit kicks in
// (mirror of PipelineGen's layout.go; the golden phrases stay under it).
const fitTextBudget = 22

// fitFontSize mirrors PipelineGen's deterministic auto-fit buckets.
func fitFontSize(text string) (float64, bool) {
	n := 0
	for _, r := range strings.Join(strings.Fields(text), " ") {
		n++
		_ = r
	}
	return fitFontSizeByRunes(n)
}

func fitFontSizeByRunes(n int) (float64, bool) {
	switch {
	case n <= fitTextBudget:
		return 0, false
	case n <= 32:
		return 56, true
	case n <= 44:
		return 48, true
	case n <= 56:
		return 40, true
	default:
		return 32, true
	}
}

// paramString reads a string param (empty when absent).
func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, ok := params[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// paramInt reads an int param (0 when absent).
func paramInt(params map[string]any, key string) int {
	if params == nil {
		return 0
	}
	switch n := params[key].(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	}
	return 0
}

// paramFloat reads a float param (0 when absent) — the planner's priority.
func paramFloat(params map[string]any, key string) float64 {
	if params == nil {
		return 0
	}
	switch n := params[key].(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0
		}
		return f
	}
	return 0
}

// paramPosition reads a numeric position override ([]float64{380, 0}).
func paramPosition(params map[string]any, key string) ([]float64, bool) {
	if params == nil {
		return nil, false
	}
	raw, ok := params[key].([]any)
	if !ok || len(raw) < 2 {
		return nil, false
	}
	out := make([]float64, 0, len(raw))
	for _, e := range raw {
		f, ok := toNumber(e)
		if !ok {
			return nil, false
		}
		out = append(out, f)
	}
	return out, true
}

// paramAnimation reads Params["animation"]["preset"].
func paramAnimation(params map[string]any) string {
	if params == nil {
		return ""
	}
	raw, ok := params["animation"].(map[string]any)
	if !ok {
		return ""
	}
	preset, _ := raw["preset"].(string)
	return preset
}

func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

func maxEnd(items []semanticItem) int64 {
	var end int64
	for _, item := range items {
		if item.EndMS > end {
			end = item.EndMS
		}
	}
	return end
}
func assetPath(raw string) string {
	base := path.Base(strings.TrimSpace(raw))
	if base == "." || base == "/" || base == "" {
		base = "asset"
	}
	return "assets/" + base
}
