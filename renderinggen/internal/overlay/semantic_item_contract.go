package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// semanticItem must remain a json.Unmarshaler. Its contract below is invoked by
// the encoding/json reflection path, so no file in this repository can name the
// call site: without the assertion a signature that drifted off the interface
// would not fail to build, it would silently stop validating every item. The
// assertion is also the only form in which the dead-export ratchet can see that
// UnmarshalJSON is wired (see stdlibInterfaceMethods in internal/architecture).
var _ json.Unmarshaler = (*semanticItem)(nil)

// UnmarshalJSON is the wire boundary for one semantic item. PipelineGen owns
// entity selection/editorial data; RenderingGen only validates and lowers it.
// Keep wire-only identity/timing fields out of the render model while still
// requiring them where the contract says they are authoritative.
func (i *semanticItem) UnmarshalJSON(data []byte) error {
	type alias semanticItem
	var decoded alias
	// DisallowUnknownFields here is load-bearing, not decoration: because
	// semanticItem implements json.Unmarshaler, the outer plan decoder hands
	// this method the item's raw bytes and its own DisallowUnknownFields stops
	// applying. A plain json.Unmarshal silently DROPPED any item-level field
	// outside the contract — including the nested map block, whose schema and
	// SemanticMap mirror forbid additional properties precisely so map
	// geometry and legal attribution can never be discarded unnoticed.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}

	*i = semanticItem(decoded)

	if i.DurationMS != nil {
		if *i.DurationMS <= 0 {
			return fmt.Errorf("overlay: item %q duration_ms must be positive", i.ID)
		}
		if want := i.EndMS - i.StartMS; *i.DurationMS != want {
			return fmt.Errorf("overlay: item %q duration_ms %d does not match end_ms-start_ms %d", i.ID, *i.DurationMS, want)
		}
	}

	spec := templateSpecFor(i.Template)
	kind, err := spec.resolveKind(i.Kind, i.ID)
	if err != nil {
		return err
	}
	// The optional metric/date payload is validated on the same pass that
	// resolves the kind, so "this block belongs to this card" is decided once,
	// by the same resolver the motion gate uses.
	if err := validateSemanticDataBlocks(*i, kind); err != nil {
		return err
	}
	if isShapeKind(kind) && i.Template != "" {
		if !spec.Registered || !isShapeKind(spec.Kind) {
			return fmt.Errorf("overlay: item %q shape template %q is not registered", i.ID, i.Template)
		}
	}
	if len(i.ImageLayers) > 0 && (kind == KindEntityImage || isImageKind(kind)) {
		// Composite image declarations carry their own independently timed
		// layers. A parent duration is not consumed by the compiler, but when
		// PipelineGen supplies it, the usual positive/matching check above
		// still applies. Require an explicit kind and preset for entity cards.
		if kind == KindEntityImage && (strings.TrimSpace(i.Kind) == "" || strings.TrimSpace(i.PresetID) == "") {
			return fmt.Errorf("overlay: composite entity image %q requires kind and preset_id from PipelineGen", i.ID)
		}
		return nil
	}
	if !isEntityKind(kind) {
		return nil
	}
	if strings.TrimSpace(i.EntityID) == "" {
		return fmt.Errorf("overlay: entity item %q requires entity_id from PipelineGen", i.ID)
	}
	if strings.TrimSpace(i.Kind) == "" {
		return fmt.Errorf("overlay: entity item %q requires kind from PipelineGen", i.ID)
	}
	if strings.TrimSpace(i.PresetID) == "" {
		return fmt.Errorf("overlay: entity item %q requires preset_id from PipelineGen", i.ID)
	}
	if strings.TrimSpace(i.Text) == "" {
		return fmt.Errorf("overlay: entity item %q requires text from PipelineGen", i.ID)
	}
	if i.DurationMS == nil {
		return fmt.Errorf("overlay: entity item %q requires duration_ms from PipelineGen", i.ID)
	}
	if len(i.Assets) > 0 && strings.TrimSpace(i.ImagePresetID) == "" {
		return fmt.Errorf("overlay: entity item %q with image requires image_preset_id from PipelineGen", i.ID)
	}
	return nil
}
