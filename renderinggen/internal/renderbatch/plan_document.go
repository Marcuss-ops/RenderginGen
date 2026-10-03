// plan_document.go owns the typed WRITER side of the renderinggen.overlay-plan.v1
// contract.
//
// It is the single writer for every command that emits a semantic plan: the
// preset batch below and the typography suite both go through BuildPlan, which
// is what keeps one wire contract from having two hand-maintained struct sets
// that drift apart (the previous arrangement: each command declared its own copy
// of the same document, with subtly different field sets).
//
// The field names and json tags mirror the worker's own decoder
// (overlay.semanticPlan), and every built document is validated by running it
// through that decoder's strict mode (DisallowUnknownFields) as part of BuildPlan
// — so this writer cannot drift from the contract it targets without a compile
// failure at the caller.
package renderbatch

import (
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// Chronon plan contract the renderer consumes. It is the ONE concrete schema a
// compiled plan may declare, so a compiler that starts emitting a different
// version fails at prepare time instead of producing a file the renderer
// silently rejects.
const (
	ChrononPlanSchema  = "chronon.render-plan.v2"
	ChrononPlanVersion = 2
	// ChrononPlanSchemaV3 is the schema Marshal() upgrades a plan to when the
	// compiled layers use V3 features (effects, masks, camera…). Chronon's
	// decoder accepts both; the batch must too, or one blur motion breaks the
	// whole matrix.
	ChrononPlanSchemaV3 = "chronon.render-plan.v3"
	// ChrononPlanVersionV3 pairs with ChrononPlanSchemaV3: Marshal() bumps the
	// version with the schema, so the batch can pin the pairing instead of
	// trusting one half of it.
	ChrononPlanVersionV3 = 3
)

// PlanSpec is a caller-supplied semantic plan to build and compile.
type PlanSpec struct {
	PlanID string
	// ProjectID and Language are the producer's delivery metadata (which project
	// the plan belongs to, which locale it carries). They travel on the plan and
	// never influence the lowering.
	ProjectID       string
	Language        string
	Width           int
	Height          int
	FPSNum          int
	FPSDen          int
	DurationMS      int64
	OutputProfileID string
	// Background is optional; nil omits the block entirely (a plan with items
	// needs no background).
	Background *Surface
	Items      []PlanItem
}

// PlanItem is one overlay item in a PlanSpec.
type PlanItem struct {
	ID string
	// SceneID is the producer's scene correlation key (which script segment the
	// item belongs to). It travels with the item and never influences lowering.
	SceneID    string
	TemplateID string
	PresetID   string
	MotionID   string
	// Kind is the semantic item kind when the producer owns it (an entity image
	// is "entity_image", not the template's default). Empty lets the template
	// registry supply the kind.
	Kind string
	// EntityID is the producer's entity correlation key. Required by the
	// contract on entity items.
	EntityID string
	// ImagePresetID is the image preset of an entity card that carries a
	// portrait asset. The contract requires it whenever an entity item declares
	// asset_refs (see semanticItem.UnmarshalJSON), so the writer must be able to
	// transport it instead of forcing callers to hand-write the plan.
	ImagePresetID string
	Text          string
	// EntityCaption is the optional caption rendered with an entity image. The
	// caption is a first-class animated layer: CaptionMotionID selects its
	// motion (empty resolves the shared default), and the compiler refuses a
	// non-text motion rather than lowering image tracks onto a text layer.
	EntityCaption   string
	CaptionMotionID string
	// DurationMS is producer-owned timing metadata. When set it must equal
	// EndMS-StartMS; the semantic decoder enforces that.
	DurationMS *int64
	// AssetRefs are the item's content-addressed media (an entity image, a
	// background plate). Empty for text-only items.
	AssetRefs []PlanAssetRef
	// MotionParams are the producer's per-item motion parameters.
	MotionParams map[string]any
	// ImageLayers are the independently timed and animated children of a
	// composite image item (the images-x2/x3/x4/x5 path). The parent item
	// declares the assets; each child names its asset_id from that set.
	ImageLayers []overlay.SemanticImageLayer
	// Map is the georeferenced map declaration of a kind=map item. It is the
	// worker's own exported type so the writer cannot describe map geometry
	// with a second field set that drifts from the decoder.
	Map *overlay.SemanticMap
	// Params carries the item's runtime controls (font family, font size,
	// glow/stroke sizes, shadow blur/opacity/offset). The worker validates them
	// against its closed runtime contract; this writer transports them and lets
	// BuildPlan's compile step reject an unsupported control before it is
	// written.
	Params map[string]any
	// Style is the item-local visual override block. Where the same key appears
	// in both, Style wins over Params — the precedence the semantic compiler
	// applies. Declared separately so a caller cannot smuggle a style key into
	// the motion-parameter block and get the other resolution order.
	Style   map[string]any
	StartMS int64
	EndMS   int64
}

// PlanAssetRef is one content-addressed asset reference on a semantic item.
type PlanAssetRef struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}

// BuildPlan builds a renderinggen.overlay-plan.v1 document from a typed spec and
// lowers it through the worker's own compiler, returning the marshaled document
// on success.
//
// Compiling here is deliberate: BuildPlan is the point at which a template,
// preset or motion that cannot resolve is reported, so no caller has to remember
// to validate what it just built.
func BuildPlan(spec PlanSpec) ([]byte, error) {
	if spec.PlanID == "" {
		return nil, fmt.Errorf("renderbatch: plan_id is required")
	}
	if spec.Width <= 0 || spec.Height <= 0 || spec.FPSNum <= 0 || spec.FPSDen <= 0 {
		return nil, fmt.Errorf("renderbatch: plan %s needs a positive canvas and fps, got %dx%d @ %d/%d",
			spec.PlanID, spec.Width, spec.Height, spec.FPSNum, spec.FPSDen)
	}
	if len(spec.Items) == 0 {
		return nil, fmt.Errorf("renderbatch: plan %s declares no items", spec.PlanID)
	}
	doc := semanticPlanDocument{
		SchemaVersion:   OverlayPlanSchema,
		PlanID:          spec.PlanID,
		VideoID:         spec.PlanID,
		ProjectID:       spec.ProjectID,
		Language:        spec.Language,
		Width:           spec.Width,
		Height:          spec.Height,
		FPSNum:          spec.FPSNum,
		FPSDen:          spec.FPSDen,
		DurationMS:      spec.DurationMS,
		OutputProfileID: spec.OutputProfileID,
		Background:      spec.Background,
		Items:           make([]semanticItemDocument, 0, len(spec.Items)),
	}
	for _, item := range spec.Items {
		doc.Items = append(doc.Items, semanticItemDocument{
			ID:              item.ID,
			SceneID:         item.SceneID,
			TemplateID:      item.TemplateID,
			PresetID:        item.PresetID,
			ImagePresetID:   item.ImagePresetID,
			MotionID:        item.MotionID,
			Kind:            item.Kind,
			EntityID:        item.EntityID,
			EntityCaption:   item.EntityCaption,
			CaptionMotionID: item.CaptionMotionID,
			DurationMS:      item.DurationMS,
			AssetRefs:       toAssetRefDocuments(item.AssetRefs),
			ImageLayers:     item.ImageLayers,
			Map:             item.Map,
			MotionParams:    item.MotionParams,
			Params:          item.Params,
			Style:           item.Style,
			Text:            item.Text,
			StartMS:         item.StartMS,
			EndMS:           item.EndMS,
		})
	}
	// Typed build, then marshal: no format string can drift from the struct tags
	// and no unescaped quote in an item's text can produce a malformed document.
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("renderbatch: encode plan %s: %w", spec.PlanID, err)
	}
	if _, err := overlay.CompileSemantic(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// CompileRenderPlan lowers a built semantic plan into the CONCRETE
// chronon.render-plan.v2 document the renderer consumes.
//
// BuildPlan deliberately returns the semantic document (RenderingGen owns the
// lowering), but `chronon3d_cli render --plan` reads the concrete one: handing it
// a semantic plan fails its schema with "required property 'layers' not found".
// The batch therefore keeps both artifacts side by side — the semantic plan as
// the input of record, the compiled plan as what is actually rendered — instead
// of leaving the renderer to reject a document this package already knows how to
// lower.
func CompileRenderPlan(raw []byte) ([]byte, error) {
	result, err := overlay.CompileSemantic(raw)
	if err != nil {
		return nil, err
	}
	if result.Plan == nil {
		return nil, fmt.Errorf("renderbatch: compiler returned no plan")
	}
	wantVersion := ChrononPlanVersion
	if result.Plan.Schema == ChrononPlanSchemaV3 {
		wantVersion = ChrononPlanVersionV3
	}
	if result.Plan.Schema != ChrononPlanSchema && result.Plan.Schema != ChrononPlanSchemaV3 {
		return nil, fmt.Errorf("renderbatch: compiler emitted %s v%d, want %s or %s",
			result.Plan.Schema, result.Plan.Version, ChrononPlanSchema, ChrononPlanSchemaV3)
	}
	if result.Plan.Version != wantVersion {
		return nil, fmt.Errorf("renderbatch: compiler emitted %s v%d, want %s paired with v%d",
			result.Plan.Schema, result.Plan.Version, result.Plan.Schema, wantVersion)
	}
	data, err := json.MarshalIndent(result.Plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("renderbatch: encode render plan: %w", err)
	}
	return append(data, '\n'), nil
}

// semanticPlanDocument is a renderinggen.overlay-plan.v1 document.
type semanticPlanDocument struct {
	SchemaVersion string `json:"schema_version"`
	PlanID        string `json:"plan_id"`
	VideoID       string `json:"video_id"`
	ProjectID     string `json:"project_id,omitempty"`
	Language      string `json:"language,omitempty"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPSNum        int    `json:"fps_num"`
	FPSDen        int    `json:"fps_den"`
	// DurationMS seeds the canvas duration; the item below extends it to the
	// same value, which is what the compiler requires for an item-bearing plan.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// Empty OutputProfileID means this batch leaves encode policy to its caller.
	OutputProfileID string `json:"output_profile_id"`
	// Background reuses the manifest's Surface type: one declaration of the
	// block, so the reader and writer cannot describe it differently.
	Background *Surface               `json:"background,omitempty"`
	Items      []semanticItemDocument `json:"items"`
}

// semanticItemDocument is one overlay item. The displayed text is owned by the
// producer: this writer never invents content, it transports the caller's.
type semanticItemDocument struct {
	ID              string                       `json:"id"`
	SceneID         string                       `json:"scene_id,omitempty"`
	TemplateID      string                       `json:"template_id"`
	PresetID        string                       `json:"preset_id"`
	ImagePresetID   string                       `json:"image_preset_id,omitempty"`
	MotionID        string                       `json:"motion_id,omitempty"`
	Kind            string                       `json:"kind,omitempty"`
	EntityID        string                       `json:"entity_id,omitempty"`
	EntityCaption   string                       `json:"entity_caption,omitempty"`
	CaptionMotionID string                       `json:"caption_motion_id,omitempty"`
	DurationMS      *int64                       `json:"duration_ms,omitempty"`
	AssetRefs       []overlay.SemanticAssetRef   `json:"asset_refs,omitempty"`
	ImageLayers     []overlay.SemanticImageLayer `json:"image_layers,omitempty"`
	Map             *overlay.SemanticMap         `json:"map,omitempty"`
	MotionParams    map[string]any               `json:"motion_params,omitempty"`
	Params          map[string]any               `json:"params,omitempty"`
	Style           map[string]any               `json:"style,omitempty"`
	Text            string                       `json:"text,omitempty"`
	StartMS         int64                        `json:"start_ms"`
	EndMS           int64                        `json:"end_ms"`
}

// toAssetRefDocuments converts the exported caller-facing refs to the worker's
// own asset_refs entry, preserving order. The document reuses
// overlay.SemanticAssetRef instead of declaring a second copy: the writer and
// the decoder must not be able to disagree about an entry, and a copy that only
// a comment claims is pinned to the other one is exactly how they do.
func toAssetRefDocuments(refs []PlanAssetRef) []overlay.SemanticAssetRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]overlay.SemanticAssetRef, 0, len(refs))
	for _, ref := range refs {
		// AssetID is the caller's spelling of the contract's asset_id.
		out = append(out, overlay.SemanticAssetRef{ID: ref.AssetID, SHA256: ref.SHA256, URL: ref.URL, MediaType: ref.MediaType})
	}
	return out
}
