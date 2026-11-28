// Package editoriallayers is the renderer boundary of the editorial
// composition contract. It decodes a lowered `editorial_layers.v1` document and
// admits it, without touching the compile path: nothing in this package is
// called by the overlay compiler yet.
//
// The split matters. PipelineGen owns the composition schema and lowers it;
// this package owns only what a renderer can prove about the lowered document:
// roles it knows, a stable time order, one background, attachments that hang
// from an existing parent, callouts anchored on a real pin with a verified
// asset, and no duplicate date. Payload blocks (chart, map, summary, chapter)
// stay opaque here: their schema belongs to the producer, not to the renderer.
package editoriallayers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	// SchemaVersion is the lowered editorial layer document version.
	SchemaVersion = "editorial_layers.v1"

	// RoleBackground is the single canvas background layer.
	RoleBackground = "background"
	// RoleImage is an entity image.
	RoleImage = "image"
	// RolePhrase is an important phrase.
	RolePhrase = "phrase"
	// RoleChart is a typed chart composition.
	RoleChart = "chart"
	// RoleMap is a map composition; it owns the georeferenced canvas.
	RoleMap = "map"
	// RoleMapCallout is a photo card anchored to a pin.
	RoleMapCallout = "map_callout"
	// RoleSummary is a local micro summary.
	RoleSummary = "summary"
	// RoleChapter is the scene-opening chapter card.
	RoleChapter = "chapter"
	// RoleDate is a standalone date card.
	RoleDate = "date"
	// RoleMetric is a metric attached to a parent.
	RoleMetric = "metric"
	// RoleCaption is a caption attached to a parent.
	RoleCaption = "caption"

	// ZBackground is the reserved z-order of the background layer.
	ZBackground = -100
)

var (
	knownRoles = map[string]struct{}{
		RoleBackground: {}, RoleImage: {}, RolePhrase: {}, RoleChart: {},
		RoleMap: {}, RoleMapCallout: {}, RoleSummary: {}, RoleChapter: {},
		RoleDate: {}, RoleMetric: {}, RoleCaption: {},
	}
	attachmentRoles = map[string]struct{}{
		RoleDate: {}, RoleMetric: {}, RoleCaption: {},
	}
	calloutPlacements = map[string]struct{}{
		"": {}, "auto": {}, "left": {}, "right": {}, "above": {}, "below": {},
	}
	hex64Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Document is one lowered editorial composition.
type Document struct {
	SchemaVersion string      `json:"schema_version"`
	CompositionID string      `json:"composition_id"`
	SceneID       string      `json:"scene_id"`
	Window        Window      `json:"window"`
	Background    *Background `json:"background,omitempty"`
	Layers        []Layer     `json:"layers"`
}

// Window is the composition extent on the canonical timeline.
type Window struct {
	StartUS    int64 `json:"start_us"`
	DurationUS int64 `json:"duration_us"`
}

// EndUS returns the exclusive end of the window.
func (w Window) EndUS() int64 { return w.StartUS + w.DurationUS }

// Background is the producer's background policy, carried verbatim.
type Background struct {
	Mode           string       `json:"mode"`
	SourceMemberID string       `json:"source_member_id,omitempty"`
	Snapshot       *SnapshotRef `json:"snapshot,omitempty"`
	Persist        bool         `json:"persist,omitempty"`
}

// SnapshotRef is a content-addressed last-frame snapshot.
type SnapshotRef struct {
	AssetID string `json:"asset_id"`
	SHA256  string `json:"sha256"`
}

// AssetRef is a verified media identity.
type AssetRef struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// Layer is one layer intent. Payload blocks are opaque on purpose.
type Layer struct {
	LayerID       string          `json:"layer_id"`
	MemberID      string          `json:"member_id,omitempty"`
	Role          string          `json:"role"`
	StartUS       int64           `json:"start_us"`
	DurationUS    int64           `json:"duration_us"`
	Z             int             `json:"z"`
	MotionID      string          `json:"motion_id,omitempty"`
	LinkGroup     string          `json:"link_group,omitempty"`
	LinkOrder     int             `json:"link_order,omitempty"`
	Hold          string          `json:"hold,omitempty"`
	Persistent    bool            `json:"persistent,omitempty"`
	ParentLayerID string          `json:"parent_layer_id,omitempty"`
	AnchorPinID   string          `json:"anchor_pin_id,omitempty"`
	Asset         *AssetRef       `json:"asset,omitempty"`
	Text          string          `json:"text,omitempty"`
	Lane          string          `json:"lane,omitempty"`
	Position      string          `json:"position,omitempty"`
	DateValue     string          `json:"date_value,omitempty"`
	Chapter       json.RawMessage `json:"chapter,omitempty"`
	Chart         json.RawMessage `json:"chart,omitempty"`
	Map           json.RawMessage `json:"map,omitempty"`
	Summary       json.RawMessage `json:"summary,omitempty"`
}

// Error is one renderer admission rejection.
type Error struct {
	Code    string
	LayerID string
	Detail  string
}

// Error implements error.
func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s at layer %s", e.Code, e.LayerID)
	}
	return fmt.Sprintf("%s at layer %s: %s", e.Code, e.LayerID, e.Detail)
}

// Admission codes.
const (
	CodeUnsupportedSchema      = "layers_unsupported_schema"
	CodeUnknownField           = "layers_unknown_field"
	CodeMissingField           = "layers_missing_field"
	CodeInvalidWindow          = "layers_invalid_window"
	CodeDuplicateLayer         = "layers_duplicate_id"
	CodeUnknownRole            = "layers_unknown_role"
	CodeOutsideWindow          = "layers_outside_window"
	CodeUnordered              = "layers_unordered"
	CodeBackgroundConflict     = "layers_background_conflict"
	CodeBackgroundZ            = "layers_background_z"
	CodeOrphanAttachment       = "layers_orphan_attachment"
	CodeCalloutUnanchored      = "layers_callout_unanchored"
	CodeCalloutAssetUnverified = "layers_callout_asset_unverified"
	CodeCalloutPlacement       = "layers_callout_placement"
	CodeMapChainForbidden      = "layers_map_chain_forbidden"
	CodePayloadMissing         = "layers_payload_missing"
	CodeDuplicateDate          = "layers_duplicate_date"
	CodeChapterWindow          = "layers_chapter_window"
)

// Decode strictly decodes a lowered layer document. An unknown field is an
// error so a producer-only field cannot appear without a renderer decision.
func Decode(raw []byte) (*Document, error) {
	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("decode layer header: %w", err)
	}
	if header.SchemaVersion != SchemaVersion {
		return nil, &Error{Code: CodeUnsupportedSchema, Detail: header.SchemaVersion}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return nil, &Error{Code: CodeUnknownField, Detail: err.Error()}
	}
	return &document, nil
}

// Plan is an admitted document plus the counts the renderer schedules with.
type Plan struct {
	Document *Document
	// RoleCounts is the number of admitted layers per role.
	RoleCounts map[string]int
	// Attachments maps a parent layer id to the attachment layer ids that hang
	// from it.
	Attachments map[string][]string
	// Callouts maps a pin id to the callout layer id anchored on it.
	Callouts map[string]string
}

// Admit decodes and admits a lowered layer document.
func Admit(raw []byte) (*Plan, error) {
	document, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	return document.Admit()
}

// Admit validates the document and returns the renderer plan.
func (d *Document) Admit() (*Plan, error) {
	if d == nil {
		return nil, &Error{Code: CodeMissingField, Detail: "document"}
	}
	if strings.TrimSpace(d.CompositionID) == "" {
		return nil, &Error{Code: CodeMissingField, Detail: "composition_id"}
	}
	if strings.TrimSpace(d.SceneID) == "" {
		return nil, &Error{Code: CodeMissingField, Detail: "scene_id"}
	}
	if d.Window.DurationUS <= 0 || d.Window.StartUS < 0 {
		return nil, &Error{Code: CodeInvalidWindow, Detail: fmt.Sprintf("start=%d duration=%d", d.Window.StartUS, d.Window.DurationUS)}
	}
	plan := &Plan{
		Document:    d,
		RoleCounts:  make(map[string]int, len(d.Layers)),
		Attachments: make(map[string][]string),
		Callouts:    make(map[string]string),
	}
	seen := make(map[string]struct{}, len(d.Layers))
	roles := make(map[string]struct{}, len(d.Layers))
	backgrounds := 0
	standaloneDate := ""
	var previousStart int64
	var previousZ int
	for index, layer := range d.Layers {
		layerID := strings.TrimSpace(layer.LayerID)
		if layerID == "" {
			return nil, &Error{Code: CodeMissingField, Detail: fmt.Sprintf("layers[%d].layer_id", index)}
		}
		if _, duplicate := seen[layerID]; duplicate {
			return nil, &Error{Code: CodeDuplicateLayer, LayerID: layerID}
		}
		seen[layerID] = struct{}{}
		if _, ok := knownRoles[layer.Role]; !ok {
			return nil, &Error{Code: CodeUnknownRole, LayerID: layerID, Detail: layer.Role}
		}
		roles[layer.Role] = struct{}{}
		if layer.DurationUS <= 0 {
			return nil, &Error{Code: CodeInvalidWindow, LayerID: layerID, Detail: fmt.Sprintf("duration=%d", layer.DurationUS)}
		}
		if layer.StartUS < d.Window.StartUS || layer.StartUS+layer.DurationUS > d.Window.EndUS() {
			return nil, &Error{Code: CodeOutsideWindow, LayerID: layerID}
		}
		if index > 0 && (layer.StartUS < previousStart || layer.StartUS == previousStart && layer.Z < previousZ) {
			return nil, &Error{Code: CodeUnordered, LayerID: layerID, Detail: "layers must be ordered by (start_us, z, layer_id)"}
		}
		previousStart, previousZ = layer.StartUS, layer.Z

		switch layer.Role {
		case RoleBackground:
			backgrounds++
			if backgrounds > 1 {
				return nil, &Error{Code: CodeBackgroundConflict, LayerID: layerID}
			}
			if layer.Z != ZBackground {
				return nil, &Error{Code: CodeBackgroundZ, LayerID: layerID, Detail: fmt.Sprintf("z=%d", layer.Z)}
			}
			if layer.Asset != nil && !verifiedAsset(layer.Asset) {
				return nil, &Error{Code: CodeCalloutAssetUnverified, LayerID: layerID, Detail: "background asset"}
			}
		case RoleImage, RolePhrase, RoleChart, RoleMap, RoleSummary:
			if err := requirePayload(layer); err != nil {
				return nil, err
			}
			if layer.Role == RoleMap && layer.LinkGroup != "" {
				return nil, &Error{Code: CodeMapChainForbidden, LayerID: layerID, Detail: layer.LinkGroup}
			}
		case RoleMapCallout:
			if strings.TrimSpace(layer.AnchorPinID) == "" {
				return nil, &Error{Code: CodeCalloutUnanchored, LayerID: layerID}
			}
			if !verifiedAsset(layer.Asset) {
				return nil, &Error{Code: CodeCalloutAssetUnverified, LayerID: layerID}
			}
			if _, ok := calloutPlacements[layer.Position]; !ok {
				return nil, &Error{Code: CodeCalloutPlacement, LayerID: layerID, Detail: layer.Position}
			}
			if _, ok := roles[RoleMap]; !ok {
				return nil, &Error{Code: CodeCalloutUnanchored, LayerID: layerID, Detail: "callout without a map layer"}
			}
			plan.Callouts[layer.AnchorPinID] = layerID
		case RoleChapter:
			if len(layer.Chapter) == 0 {
				return nil, &Error{Code: CodePayloadMissing, LayerID: layerID, Detail: "chapter"}
			}
			if layer.StartUS != d.Window.StartUS {
				return nil, &Error{Code: CodeChapterWindow, LayerID: layerID, Detail: "a chapter opens its composition"}
			}
		case RoleDate, RoleMetric, RoleCaption:
			if layer.Role == RoleDate && strings.TrimSpace(layer.ParentLayerID) == "" {
				// A standalone date card is not an attachment: it keeps its own
				// window and has no parent.
				standaloneDate = layerID
				break
			}
			if strings.TrimSpace(layer.ParentLayerID) == "" {
				return nil, &Error{Code: CodeOrphanAttachment, LayerID: layerID, Detail: "attachment without parent"}
			}
			if _, ok := seen[layer.ParentLayerID]; !ok {
				// The parent must appear earlier: layers are time ordered and a
				// child never precedes its own parent.
				return nil, &Error{Code: CodeOrphanAttachment, LayerID: layerID, Detail: layer.ParentLayerID}
			}
			plan.Attachments[layer.ParentLayerID] = append(plan.Attachments[layer.ParentLayerID], layerID)
		}
		plan.RoleCounts[layer.Role]++
	}
	if standaloneDate != "" && hasAttachmentDate(d.Layers) {
		return nil, &Error{Code: CodeDuplicateDate, LayerID: standaloneDate}
	}
	return plan, nil
}

func requirePayload(layer Layer) error {
	switch layer.Role {
	case RoleChart:
		if len(layer.Chart) == 0 {
			return &Error{Code: CodePayloadMissing, LayerID: layer.LayerID, Detail: "chart"}
		}
	case RoleMap:
		if len(layer.Map) == 0 {
			return &Error{Code: CodePayloadMissing, LayerID: layer.LayerID, Detail: "map"}
		}
	case RoleSummary:
		if len(layer.Summary) == 0 {
			return &Error{Code: CodePayloadMissing, LayerID: layer.LayerID, Detail: "summary"}
		}
	}
	return nil
}

func hasAttachmentDate(layers []Layer) bool {
	for _, layer := range layers {
		if layer.Role == RoleDate && layer.ParentLayerID != "" {
			return true
		}
	}
	return false
}

func verifiedAsset(asset *AssetRef) bool {
	if asset == nil {
		return false
	}
	return strings.TrimSpace(asset.AssetID) != "" && hex64Pattern.MatchString(asset.SHA256)
}
