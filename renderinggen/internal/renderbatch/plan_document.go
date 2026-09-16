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
	Text     string
	// DurationMS is producer-owned timing metadata. When set it must equal
	// EndMS-StartMS; the semantic decoder enforces that.
	DurationMS *int64
	// AssetRefs are the item's content-addressed media (an entity image, a
	// background plate). Empty for text-only items.
	AssetRefs []PlanAssetRef
	// MotionParams are the producer's per-item motion parameters.
	MotionParams map[string]any
	StartMS      int64
	EndMS        int64
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
			ID:           item.ID,
			SceneID:      item.SceneID,
			TemplateID:   item.TemplateID,
			PresetID:     item.PresetID,
			MotionID:     item.MotionID,
			Kind:         item.Kind,
			EntityID:     item.EntityID,
			DurationMS:   item.DurationMS,
			AssetRefs:    toAssetRefDocuments(item.AssetRefs),
			MotionParams: item.MotionParams,
			Text:         item.Text,
			StartMS:      item.StartMS,
			EndMS:        item.EndMS,
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
	if result.Plan.Schema != ChrononPlanSchema || result.Plan.Version != ChrononPlanVersion {
		return nil, fmt.Errorf("renderbatch: compiler emitted %s v%d, want %s v%d",
			result.Plan.Schema, result.Plan.Version, ChrononPlanSchema, ChrononPlanVersion)
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
	// OutputProfileID / StyleProfile are part of the published contract. Empty
	// means "no output profile requested", which is what this batch renders.
	OutputProfileID string `json:"output_profile_id"`
	StyleProfile    string `json:"style_profile"`
	// Background reuses the manifest's Surface type: one declaration of the
	// block, so the reader and writer cannot describe it differently.
	Background *Surface               `json:"background,omitempty"`
	Items      []semanticItemDocument `json:"items"`
}

// semanticItemDocument is one overlay item. The displayed text is owned by the
// producer: this writer never invents content, it transports the caller's.
type semanticItemDocument struct {
	ID           string             `json:"id"`
	SceneID      string             `json:"scene_id,omitempty"`
	TemplateID   string             `json:"template_id"`
	PresetID     string             `json:"preset_id"`
	MotionID     string             `json:"motion_id,omitempty"`
	Kind         string             `json:"kind,omitempty"`
	EntityID     string             `json:"entity_id,omitempty"`
	DurationMS   *int64             `json:"duration_ms,omitempty"`
	AssetRefs    []semanticAssetRef `json:"asset_refs,omitempty"`
	MotionParams map[string]any     `json:"motion_params,omitempty"`
	Text         string             `json:"text,omitempty"`
	StartMS      int64              `json:"start_ms"`
	EndMS        int64              `json:"end_ms"`
}

// semanticAssetRef mirrors the worker's asset_refs entry. It is declared here
// (not aliased) because the worker's type is unexported; the field set is pinned
// to it by the parity test.
type semanticAssetRef struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}

// toAssetRefDocuments converts the exported caller-facing refs to the document
// shape, preserving order.
func toAssetRefDocuments(refs []PlanAssetRef) []semanticAssetRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]semanticAssetRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, semanticAssetRef{AssetID: ref.AssetID, SHA256: ref.SHA256, URL: ref.URL, MediaType: ref.MediaType})
	}
	return out
}
