package overlay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// PreparedPackage is the immutable preparation result shared by all frames of
// one compiled overlay plan. It deliberately contains no translated source
// text or asset lookup policy: PipelineGen has already supplied the final
// localized text and the semantic compiler has already resolved every asset.
// Backends may upload each PreparedAsset/PreparedTextRun once and only consume
// PreparedOverlay.Dynamic while rendering frames.
type PreparedPackage struct {
	PlanID      string            `json:"plan_id"`
	Language    string            `json:"language,omitempty"`
	Assets      []PreparedAsset   `json:"assets,omitempty"`
	TextRuns    []PreparedTextRun `json:"text_runs,omitempty"`
	Overlays    []PreparedOverlay `json:"overlays"`
	ContentHash string            `json:"content_hash"`
}

// PreparedAsset is a content-addressed image/video input. Size and fit are
// part of the preparation identity: the same bytes with a different resolved
// target size must not accidentally share a resized GPU surface.
type PreparedAsset struct {
	Key         string    `json:"key"`
	Kind        string    `json:"kind"`
	ContentHash string    `json:"content_hash"`
	LogicalPath string    `json:"logical_path"`
	Size        []float64 `json:"size,omitempty"`
	Fit         string    `json:"fit,omitempty"`
}

// PreparedTextRun is the single static identity for one complete localized
// phrase. Fill and stroke are included in the key so the same shaped run can
// be reused only when its visual paint is genuinely identical.
type PreparedTextRun struct {
	Key         string      `json:"key"`
	Text        string      `json:"text"`
	Font        string      `json:"font,omitempty"`
	FontSize    float64     `json:"font_size,omitempty"`
	Fill        string      `json:"fill,omitempty"`
	StrokeColor string      `json:"stroke_color,omitempty"`
	StrokeWidth float64     `json:"stroke_width,omitempty"`
	Size        []float64   `json:"size,omitempty"`
	Style       *LayerStyle `json:"style,omitempty"`
}

// DynamicOverlayState is the only per-frame-facing portion of a prepared
// overlay. Timing, transform, opacity and already-compiled motion selectors
// may be evaluated per frame; the underlying text/image preparation is not.
type DynamicOverlayState struct {
	StartFrame     int64           `json:"start_frame"`
	DurationFrames int64           `json:"duration_frames"`
	Position       []float64       `json:"position,omitempty"`
	Scale          []float64       `json:"scale,omitempty"`
	Opacity        *float64        `json:"opacity,omitempty"`
	Animation      *LayerAnimation `json:"animation,omitempty"`
	TextAnimators  []TextAnimator  `json:"text_animators,omitempty"`
}

type PreparedOverlay struct {
	ID       string              `json:"id"`
	Kind     string              `json:"kind"`
	TextKey  string              `json:"text_key,omitempty"`
	AssetKey string              `json:"asset_key,omitempty"`
	Static   bool                `json:"static"`
	Dynamic  DynamicOverlayState `json:"dynamic"`
}

// Validate checks the immutable package at the boundary where it is handed
// to the staged worker. This keeps a corrupt or stale sidecar from becoming a
// renderer-specific failure and makes the content hash an actual contract,
// not just an informational field.
func (p PreparedPackage) Validate() error {
	if p.PlanID == "" {
		return fmt.Errorf("overlay: prepared package has no plan id")
	}
	if p.ContentHash == "" {
		return fmt.Errorf("overlay: prepared package has no content hash")
	}
	textKeys := make(map[string]struct{}, len(p.TextRuns))
	for _, run := range p.TextRuns {
		if run.Key == "" || run.Text == "" {
			return fmt.Errorf("overlay: prepared text run is missing key or complete text")
		}
		if _, exists := textKeys[run.Key]; exists {
			return fmt.Errorf("overlay: duplicate prepared text key %q", run.Key)
		}
		textKeys[run.Key] = struct{}{}
	}
	assetKeys := make(map[string]struct{}, len(p.Assets))
	for _, asset := range p.Assets {
		if asset.Key == "" || asset.ContentHash == "" || asset.LogicalPath == "" {
			return fmt.Errorf("overlay: prepared asset %q is missing identity or path", asset.Key)
		}
		if _, exists := assetKeys[asset.Key]; exists {
			return fmt.Errorf("overlay: duplicate prepared asset key %q", asset.Key)
		}
		assetKeys[asset.Key] = struct{}{}
	}
	for _, overlay := range p.Overlays {
		switch overlay.Kind {
		case "text":
			if _, exists := textKeys[overlay.TextKey]; !exists {
				return fmt.Errorf("overlay: %q references unknown text key %q", overlay.ID, overlay.TextKey)
			}
		case "image", "video":
			if _, exists := assetKeys[overlay.AssetKey]; !exists {
				return fmt.Errorf("overlay: %q references unknown asset key %q", overlay.ID, overlay.AssetKey)
			}
		}
	}
	canonical := p
	canonical.ContentHash = ""
	digest, err := stableDigest(canonical)
	if err != nil {
		return fmt.Errorf("overlay: validate content identity: %w", err)
	}
	if digest != p.ContentHash {
		return fmt.Errorf("overlay: content hash mismatch: got %s want %s", p.ContentHash, digest)
	}
	return nil
}

type textPreparationIdentity struct {
	Text  string      `json:"text"`
	Style *LayerStyle `json:"style,omitempty"`
	Size  []float64   `json:"size,omitempty"`
}

type assetPreparationIdentity struct {
	ContentHash string    `json:"content_hash"`
	Kind        string    `json:"kind"`
	Size        []float64 `json:"size,omitempty"`
	Fit         string    `json:"fit,omitempty"`
}

func buildPreparedPackage(plan *Plan, language string, assets []Asset) (PreparedPackage, error) {
	if plan == nil {
		return PreparedPackage{}, fmt.Errorf("overlay: cannot prepare a nil plan")
	}
	assetHashes := make(map[string]string, len(assets))
	for _, asset := range assets {
		assetHashes[asset.LogicalPath] = asset.Hash
	}

	pkg := PreparedPackage{PlanID: plan.JobID, Language: language}
	textByKey := make(map[string]struct{})
	assetByKey := make(map[string]int)
	for _, layer := range plan.Layers {
		prepared := PreparedOverlay{
			ID:   layer.ID,
			Kind: layer.Type,
			Dynamic: DynamicOverlayState{
				StartFrame:     layer.StartFrame,
				DurationFrames: layer.DurationFrames,
				Position:       cloneFloats(layer.Position),
				Scale:          cloneFloats(layer.Scale),
				Opacity:        cloneFloat(layer.Opacity),
				Animation:      layer.Animation,
				TextAnimators:  layer.TextAnimators,
			},
		}
		prepared.Static = layer.Animation == nil && len(layer.TextAnimators) == 0

		switch layer.Type {
		case "text":
			identity := textPreparationIdentity{Text: layer.Text, Style: layer.Style, Size: cloneFloats(layer.Size)}
			key, err := stableDigest(identity)
			if err != nil {
				return PreparedPackage{}, fmt.Errorf("overlay: text preparation identity %q: %w", layer.ID, err)
			}
			prepared.TextKey = key
			if _, exists := textByKey[key]; !exists {
				pkg.TextRuns = append(pkg.TextRuns, PreparedTextRun{
					Key: key, Text: layer.Text, Font: layerStyleFont(layer.Style),
					FontSize: layerStyleFontSize(layer.Style), Fill: layerStyleFill(layer.Style),
					StrokeColor: layerStyleStrokeColor(layer.Style), StrokeWidth: layerStyleStrokeWidth(layer.Style),
					Size: cloneFloats(layer.Size), Style: layer.Style,
				})
				textByKey[key] = struct{}{}
			}
		case "image", "video":
			logicalPath := layer.Asset
			if logicalPath == "" {
				logicalPath = layer.Source
			}
			if logicalPath == "" {
				continue
			}
			identity := assetPreparationIdentity{ContentHash: assetHashes[logicalPath], Kind: layer.Type, Size: cloneFloats(layer.Size), Fit: layer.Fit}
			key, err := stableDigest(identity)
			if err != nil {
				return PreparedPackage{}, fmt.Errorf("overlay: asset preparation identity %q: %w", layer.ID, err)
			}
			prepared.AssetKey = key
			if _, exists := assetByKey[key]; !exists {
				pkg.Assets = append(pkg.Assets, PreparedAsset{Key: key, Kind: layer.Type, ContentHash: identity.ContentHash, LogicalPath: logicalPath, Size: cloneFloats(layer.Size), Fit: layer.Fit})
				assetByKey[key] = len(pkg.Assets) - 1
			} else if index := assetByKey[key]; logicalPath < pkg.Assets[index].LogicalPath {
				// The upload/cache identity is content-based; retain one
				// deterministic representative path for diagnostics and any
				// consumer that still needs to locate the bytes.
				pkg.Assets[index].LogicalPath = logicalPath
			}
		}
		pkg.Overlays = append(pkg.Overlays, prepared)
	}

	sort.Slice(pkg.TextRuns, func(i, j int) bool { return pkg.TextRuns[i].Key < pkg.TextRuns[j].Key })
	sort.Slice(pkg.Assets, func(i, j int) bool { return pkg.Assets[i].Key < pkg.Assets[j].Key })
	canonical := pkg
	canonical.ContentHash = ""
	digest, err := stableDigest(canonical)
	if err != nil {
		return PreparedPackage{}, fmt.Errorf("overlay: package content identity: %w", err)
	}
	pkg.ContentHash = digest
	if err := pkg.Validate(); err != nil {
		return PreparedPackage{}, err
	}
	return pkg, nil
}

// PreparePackage rebuilds the immutable sidecar from the final concrete plan.
//
// CompileSemantic calls this during the initial semantic lowering, but later
// processor stages may append concrete layers (for example ASS subtitle
// burn-in).  Those stages must not keep a stale package whose overlay list no
// longer matches plan.Layers: Chronon validates that relationship fail-closed
// before compiling the GPU program.
func PreparePackage(plan *Plan, language string, assets []Asset) (PreparedPackage, error) {
	return buildPreparedPackage(plan, language, assets)
}

func stableDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func cloneFloats(in []float64) []float64 {
	if in == nil {
		return nil
	}
	return append([]float64(nil), in...)
}

func cloneFloat(in *float64) *float64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func layerStyleFont(style *LayerStyle) string {
	if style == nil {
		return ""
	}
	return style.Font
}
func layerStyleFontSize(style *LayerStyle) float64 {
	if style == nil {
		return 0
	}
	return style.FontSize
}
func layerStyleFill(style *LayerStyle) string {
	if style == nil {
		return ""
	}
	return style.Fill
}
func layerStyleStrokeColor(style *LayerStyle) string {
	if style == nil || style.Stroke == nil {
		return ""
	}
	return style.Stroke.Color
}
func layerStyleStrokeWidth(style *LayerStyle) float64 {
	if style == nil || style.Stroke == nil {
		return 0
	}
	return style.Stroke.Width
}
