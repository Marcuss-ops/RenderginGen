package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The selection model is a compiled VIEW of facts other owners already state.
// These tests pin two things at once: the shape a picker consumes, and the fact
// that the published cells agree with the compiler. A cell that claims
// availability the compiler refuses — or hides a support it does not have — is a
// product bug, not a documentation typo.

func TestSelectionModelPublishesTheSixProductZones(t *testing.T) {
	zones := runtimeZones()
	want := []string{ZoneImages, ZoneImagesWithText, ZoneMaps, ZoneBackground, ZoneData, ZoneCameraRoll}
	if len(zones) != len(want) {
		t.Fatalf("selection model declares %d zones, want %d", len(zones), len(want))
	}
	byID := make(map[string]RuntimeZone, len(zones))
	owner := make(map[string]string)
	for index, zone := range zones {
		if zone.ID != want[index] {
			t.Errorf("zone %d = %q, want %q", index, zone.ID, want[index])
		}
		if zone.Label == "" || zone.Description == "" {
			t.Errorf("zone %q lacks a label or description", zone.ID)
		}
		if zone.Status != zoneStatusAvailable && zone.Status != zoneStatusNoComposition {
			t.Errorf("zone %q status = %q", zone.ID, zone.Status)
		}
		byID[zone.ID] = zone
		for _, id := range zone.CompositionIDs {
			if other, taken := owner[id]; taken {
				t.Errorf("composition %q appears in both %q and %q", id, other, zone.ID)
			}
			owner[id] = zone.ID
		}
	}
	// Every composition the compiler knows must be reachable from exactly one
	// zone: a composition no zone lists is invisible in the picker.
	for _, definition := range imageCompositionCatalog {
		if definition.Zone != ZoneImages && definition.Zone != ZoneImagesWithText {
			t.Errorf("image composition %q declares unknown zone %q", definition.ID, definition.Zone)
		}
		if owner[definition.ID] != definition.Zone {
			t.Errorf("image composition %q zone = %q, want its catalog zone %q", definition.ID, owner[definition.ID], definition.Zone)
		}
	}
	for _, definition := range mapCompositionCatalog {
		if definition.Zone != ZoneMaps || owner[definition.ID] != definition.Zone {
			t.Errorf("map composition %q zone = %q, want its catalog zone %q", definition.ID, owner[definition.ID], definition.Zone)
		}
	}
	for _, definition := range backgroundCompositionCatalog {
		if definition.Zone != ZoneBackground || owner[definition.ID] != definition.Zone {
			t.Errorf("background composition %q zone = %q, want its catalog zone %q", definition.ID, owner[definition.ID], definition.Zone)
		}
	}
	for _, id := range []string{"metric_stat", "timeline_date"} {
		if owner[id] != ZoneData {
			t.Errorf("data composition %q zone = %q, want %q", id, owner[id], ZoneData)
		}
	}
	if got := byID[ZoneCameraRoll]; got.Status != zoneStatusNoComposition || len(got.CompositionIDs) != 0 {
		t.Errorf("camera_roll must be declared without a selectable composition yet: %+v", got)
	}
}

func TestSelectionModelLayoutsCoverEveryImageComposition(t *testing.T) {
	layouts := runtimeCompositionLayouts()
	if len(layouts) != len(imageCompositionCatalog) {
		t.Fatalf("layouts = %d, want one per image composition (%d)", len(layouts), len(imageCompositionCatalog))
	}
	byComposition := make(map[string]RuntimeCompositionLayout, len(layouts))
	for _, layout := range layouts {
		if _, exists := byComposition[layout.CompositionID]; exists {
			t.Fatalf("duplicate layout for composition %q", layout.CompositionID)
		}
		byComposition[layout.CompositionID] = layout
		if layout.Zone == "" {
			t.Errorf("layout %q is not assigned to a zone", layout.ID)
		}
		if layout.SafeAreaFraction != AnchorSafeAreaFraction {
			t.Errorf("layout %q safe area = %v, want the design token %v", layout.ID, layout.SafeAreaFraction, AnchorSafeAreaFraction)
		}
		if layout.CropMode != compositionCropMode || layout.MissingAsset != compositionMissingAsset || layout.LayerOrder != compositionLayerOrder {
			t.Errorf("layout %q does not publish the enforced crop/missing-asset/order rule: %+v", layout.ID, layout)
		}
	}
	for _, definition := range imageCompositionCatalog {
		layout, ok := byComposition[definition.ID]
		if !ok {
			t.Errorf("composition %q has no layout", definition.ID)
			continue
		}
		wantActive := compositionActiveLayerSingle
		wantScope := compositionMotionScopeItem
		if definition.Composition.ImageCount > 1 {
			wantActive = compositionActiveLayerMulti
			wantScope = compositionMotionScopePerLayer
		}
		if layout.ActiveLayer != wantActive {
			t.Errorf("composition %q active_layer = %q, want %q", definition.ID, layout.ActiveLayer, wantActive)
		}
		if layout.MotionScope != wantScope {
			t.Errorf("composition %q motion_scope = %q, want %q", definition.ID, layout.MotionScope, wantScope)
		}
		if caption := definition.Composition.Caption; caption != nil {
			wantLines := captionTextLimitLines(caption.Maximum)
			if layout.MaxCaptions != caption.Maximum || layout.CaptionEntryOrder != compositionCaptionEntryOrder || layout.TextLimitLines != wantLines {
				t.Errorf("composition %q caption layout = %+v, want max %d in declaration order with %d lines", definition.ID, layout, caption.Maximum, wantLines)
			}
			if layout.CaptionMotionScope != compositionMotionScopePerCaption {
				t.Errorf("composition %q caption_motion_scope = %q, want %q", definition.ID, layout.CaptionMotionScope, compositionMotionScopePerCaption)
			}
			if layout.CaptionUseCase != compositionCaptionUseCase {
				t.Errorf("composition %q caption_use_case = %q, want %q", definition.ID, layout.CaptionUseCase, compositionCaptionUseCase)
			}
		} else if layout.MaxCaptions != 0 || layout.CaptionEntryOrder != "" || layout.CaptionMotionScope != "" || layout.CaptionUseCase != "" {
			t.Errorf("image-only composition %q must not publish caption rules: %+v", definition.ID, layout)
		}
	}
}

func TestSelectionModelCompatibilityMatrixMatchesTheCatalog(t *testing.T) {
	options, motionDefinitions := runtimeMotionCatalog()
	useCases := runtimeAnimationUseCases(options, motionDefinitions)
	_, definitions := runtimeMotionCatalog()
	entries := runtimeCompatibilityMatrix(useCases, definitions)
	if len(entries) != len(useCases) {
		t.Fatalf("matrix rows = %d, want one per use case (%d)", len(entries), len(useCases))
	}
	knownCapabilities := map[string]bool{capability2D: true, capability3D: true, capabilityCamera: true, capabilityGeospatial: true}
	byComposition := make(map[string]RuntimeCompatibilityEntry, len(entries))
	for _, entry := range entries {
		byComposition[entry.CompositionID] = entry
		if entry.MotionTarget == "" {
			t.Errorf("matrix row %q has no motion target", entry.CompositionID)
		}
		if len(entry.RequiredCapabilities) == 0 {
			t.Errorf("matrix row %q declares no renderer capability", entry.CompositionID)
		}
		for _, capability := range entry.RequiredCapabilities {
			if !knownCapabilities[capability] {
				t.Errorf("matrix row %q declares unknown capability %q", entry.CompositionID, capability)
			}
		}
		if entry.Available && len(entry.MotionFamilies) == 0 {
			t.Errorf("available matrix row %q names no canonical family: %+v", entry.CompositionID, entry)
		}
		if !entry.Available && len(entry.MotionFamilies) != 0 {
			t.Errorf("unavailable matrix row %q names families it cannot expose: %+v", entry.CompositionID, entry)
		}
		if entry.Available && entry.UnavailableReason != "" {
			t.Errorf("available row %q still carries an unavailable reason", entry.CompositionID)
		}
		if !entry.Available && entry.UnavailableReason == "" {
			t.Errorf("unavailable row %q does not explain why it is not selectable", entry.CompositionID)
		}
	}
	for _, useCase := range useCases {
		entry, ok := byComposition[useCase.ID]
		if !ok {
			t.Errorf("use case %q is missing from the compatibility matrix", useCase.ID)
			continue
		}
		if entry.Available != (len(useCase.MotionIDs) > 0) || entry.MotionCount != len(useCase.MotionIDs) {
			t.Errorf("matrix row %q disagrees with the picker: %+v vs %d motions", useCase.ID, entry, len(useCase.MotionIDs))
		}
		if useCase.Zone != runtimeUseCaseZone(useCase.ID) {
			t.Errorf("use case %q zone = %q, want %q", useCase.ID, useCase.Zone, runtimeUseCaseZone(useCase.ID))
		}
	}
	// The geospatial plane is not optional on a map viewport, and the cells the
	// catalog publishes as null must stay unavailable in the matrix.
	if entry := byComposition["one_map"]; !containsString(entry.RequiredCapabilities, capabilityGeospatial) || !entry.Available {
		t.Errorf("one_map matrix row = %+v, want an available geospatial cell", entry)
	}
	// Every counted composition now selects the canonical item motions per
	// layer or per plate; only the plan-owned background sources still have no
	// family of their own, and they must stay unavailable with a reason.
	for _, id := range []string{
		"image_double", "image_triplet", "image_four", "image_five",
		"single_image_with_text", "image_double_with_text", "image_triplet_with_text", "image_four_with_text", "image_five_with_text",
		"two_maps",
	} {
		entry, ok := byComposition[id]
		if !ok {
			t.Errorf("composition %q is missing from the compatibility matrix", id)
			continue
		}
		if !entry.Available || entry.MotionCount == 0 {
			t.Errorf("composition %q must expose the admitted motions of its own target: %+v", id, entry)
		}
	}
	for _, id := range []string{"background_color", "background_image", "background_video"} {
		if entry := byComposition[id]; entry.Available || entry.MotionCount != 0 {
			t.Errorf("background composition %q must stay unavailable in the matrix: %+v", id, entry)
		}
	}
}

func TestSelectionModelMapLayoutsDeclareSupportAndProjection(t *testing.T) {
	supported := make(map[string]bool)
	for _, layout := range runtimeMapLayouts() {
		if layout.CompositionID != "one_map" {
			t.Errorf("map layout %q is attached to unsupported composition %q", layout.ID, layout.CompositionID)
		}
		if layout.Description == "" {
			t.Errorf("map layout %q has no description", layout.ID)
		}
		if layout.Supported {
			supported[layout.ID] = true
			for _, target := range layout.ProjectedMotionTargets {
				if target != "map_view" {
					t.Errorf("map layout %q projects unknown target %q", layout.ID, target)
				}
			}
			continue
		}
		if layout.UnavailableReason == "" {
			t.Errorf("unsupported map layout %q does not explain the missing lowering", layout.ID)
		}
		if len(layout.ProjectedMotionTargets) != 0 {
			t.Errorf("unsupported map layout %q must not advertise projected motion", layout.ID)
		}
	}
	for _, id := range []string{"basemap", "pins", "pin_labels", "attribution", "camera_fly_to"} {
		if !supported[id] {
			t.Errorf("map layout %q should be supported by the current lowering", id)
		}
	}
	if !supported["route"] {
		t.Error("route has a certified great-circle path/trim lowering and should be supported")
	}
	for _, id := range []string{"stops", "callout"} {
		if supported[id] {
			t.Errorf("map layout %q has no lowering but is declared supported", id)
		}
	}
}

// TestSelectionModelBackgroundSourcesMatchCompilerAdmission is the cross-check
// that matters for the Background zone: every source the model publishes as
// supported must compile, and every source it publishes as unavailable must be
// refused by the lowering with its diagnostic.
func TestSelectionModelBackgroundSourcesMatchCompilerAdmission(t *testing.T) {
	sources := runtimeBackgroundSources()
	if len(sources) == 0 {
		t.Fatal("selection model published no background source")
	}
	priorities := make(map[int]bool)
	seen := make(map[string]bool)
	for _, source := range sources {
		if seen[source.Kind] {
			t.Fatalf("background source %q is published twice", source.Kind)
		}
		seen[source.Kind] = true
		if source.Coverage != "full_canvas" {
			t.Errorf("background source %q coverage = %q", source.Kind, source.Coverage)
		}
		if !source.Supported {
			if source.UnavailableReason == "" {
				t.Errorf("unsupported background source %q has no reason", source.Kind)
			}
			continue
		}
		if source.CompositionID == "" || backgroundCompositionFor(source.CompositionID) == nil {
			t.Errorf("supported background source %q is not backed by a composition record", source.Kind)
		}
		priorities[source.Priority] = true
	}
	var declared string
	for _, definition := range backgroundCompositionCatalog {
		declared += definition.Kind + "\n"
	}
	for _, source := range sources {
		if !source.Supported {
			continue
		}
		if !strings.Contains(declared, source.Kind+"\n") {
			t.Errorf("source %q is published as supported but no composition record declares it", source.Kind)
		}
	}

	plans := map[string]string{
		"color":     fmt.Sprintf(`{"kind":"color","color":%s}`, certificationBackgroundRGBA),
		"image":     backgroundAssetJSON("image", "assets/semantic/bg-plate/background.png", "image/png"),
		"video":     backgroundAssetJSON("video", "assets/semantic/bg-plate/background.mp4", "video/mp4"),
		"gradient":  `{"kind":"gradient"}`,
		"pattern":   `{"kind":"pattern"}`,
		"generated": `{"kind":"generated"}`,
	}
	for _, source := range sources {
		plan, ok := plans[source.Kind]
		if !ok {
			t.Errorf("background source %q has no admission probe; the cross-check would silently pass", source.Kind)
			continue
		}
		_, err := CompileSemantic([]byte(backgroundOnlyPlanJSON(plan)))
		if source.Supported && err != nil {
			t.Errorf("source %q is published as supported but does not compile: %v", source.Kind, err)
		}
		if !source.Supported {
			if err == nil {
				t.Errorf("source %q is published as unavailable but the compiler accepted it", source.Kind)
				continue
			}
			if !strings.Contains(err.Error(), "unsupported background kind") {
				t.Errorf("source %q rejection = %v, want the unsupported-kind diagnostic", source.Kind, err)
			}
		}
	}
}

func TestSelectionModelCameraDestinationsSeparateImplementedFromBlocked(t *testing.T) {
	destinations := runtimeCameraDestinations()
	if len(destinations) != 3 {
		t.Fatalf("camera destinations = %d, want scene, image and map", len(destinations))
	}
	byID := make(map[string]RuntimeCameraDestination, len(destinations))
	for _, destination := range destinations {
		byID[destination.ID] = destination
		if destination.RuntimeSupport == "" || destination.Description == "" {
			t.Errorf("camera destination %q is incomplete: %+v", destination.ID, destination)
		}
		if destination.RequiresProductDecision && destination.Status != "blocked_product_decision" {
			t.Errorf("camera destination %q requires a decision but reports %q", destination.ID, destination.Status)
		}
	}
	// GOAL-2 closure: whole-scene camera is out of scope — published as
	// unsupported with no movement contract (fail-closed, no selectable IDs).
	if scene := byID["scene"]; scene.RequiresProductDecision || scene.Status != cameraDestinationUnsupported || len(scene.Movements) != 0 {
		t.Errorf("scene camera must report unsupported with no movements: %+v", scene)
	}
	if image := byID["image"]; image.Status != "implemented" || image.MotionTarget != "image" {
		t.Errorf("image camera destination = %+v", image)
	}
	if mapped := byID["map"]; mapped.Status != "implemented" || mapped.MotionTarget != "map_view" {
		t.Errorf("map camera destination = %+v", mapped)
	}
	policy := RuntimeCameraPolicy{MaxSceneControllers: cameraSceneControllerLimit, Rule: cameraPrecedenceRule}
	if policy.MaxSceneControllers != 1 || policy.Rule == "" {
		t.Fatalf("camera policy does not publish the enforced single-controller rule: %+v", policy)
	}
}

// TestSelectionModelDataFieldsAreEnforced pins the catalog's data_fields block
// to the compiler. Enforced is a claim about the wire contract: every published
// field must be carried by overlay-plan.v1 and validated when declared, and a
// declared block that omits a required field must fail. The probe below is the
// claim in executable form — it stops being honest the moment the contract and
// the compiler diverge.
func TestSelectionModelDataFieldsAreEnforced(t *testing.T) {
	fields := runtimeDataFields()
	required := map[string]map[string]bool{"metric_stat": {}, "timeline_date": {}}
	for _, field := range fields {
		if _, ok := required[field.CompositionID]; !ok {
			t.Errorf("data field %q targets unknown composition %q", field.Name, field.CompositionID)
		}
		if field.Description == "" {
			t.Errorf("data field %q.%q has no description", field.CompositionID, field.Name)
		}
		if field.Required {
			required[field.CompositionID][field.Name] = true
		}
		if !field.Enforced {
			t.Errorf("data field %q.%q is published but not marked enforced", field.CompositionID, field.Name)
		}
	}
	for _, name := range []string{"value", "unit"} {
		if !required["metric_stat"][name] {
			t.Errorf("metric_stat.%s must be declared required", name)
		}
	}
	if !required["timeline_date"]["value"] {
		t.Error("timeline_date.value must be declared required")
	}

	// Every declared field the catalog publishes must survive the wire. The
	// probe sends the full metric payload; the date payload is covered by
	// semantic_data_contract_test.go.
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"metric-probe","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,` +
		`"items":[{"id":"metric","kind":"metric_stat","template_id":"METRIC_STAT_CARD","preset_id":"phrase_default","start_ms":0,"end_ms":2000,"text":"42%",` +
		`"metric":{"value":"42","unit":"%","label":"Growth","delta":"+4","precision":1}}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("declared metric block must compile, got %v", err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].Type != "text" {
		t.Fatalf("metric card lowered to unexpected layers: %+v", result.Plan.Layers)
	}

	// The enforced half: the same block without unit must fail closed with a
	// diagnostic naming the field the catalog marks required.
	incomplete := bytes.Replace(raw, []byte(`"unit":"%",`), nil, 1)
	if _, err := CompileSemantic(incomplete); err == nil || !strings.Contains(err.Error(), "metric.unit") {
		t.Fatalf("a declared metric block without unit must fail on metric.unit, got %v", err)
	}
}

func TestSelectionModelIsDeterministicAndSchemaValidated(t *testing.T) {
	var first, second bytes.Buffer
	if err := WriteRuntimeAnimationCatalog(&first); err != nil {
		t.Fatal(err)
	}
	if err := WriteRuntimeAnimationCatalog(&second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("selection model is not deterministic")
	}
	var decoded RuntimeAnimationCatalog
	if err := json.Unmarshal(first.Bytes(), &decoded); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	model := decoded.SelectionModel
	if len(model.Zones) != 6 || len(model.Compatibility) == 0 || len(model.Layouts) == 0 || len(model.MapLayouts) == 0 ||
		len(model.BackgroundSources) == 0 || len(model.CameraDestinations) == 0 || len(model.DataFields) == 0 {
		t.Fatalf("selection model is incomplete: %+v", model)
	}
	for _, key := range []string{`"selection_model"`, `"compatibility"`, `"background_sources"`, `"camera_destinations"`, `"data_fields"`} {
		if !strings.Contains(first.String(), key) {
			t.Errorf("compiled payload omitted %s", key)
		}
	}
}

// TestSelectionModelLayerOrderAndCaptionRulesAreEnforced keeps the layout
// metadata honest: the rules it publishes are the ones the compiler applies.
func TestSelectionModelLayerOrderAndCaptionRulesAreEnforced(t *testing.T) {
	layers := `[
		{"id":"one","asset_id":"a","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","params":{"width":420,"height":360,"position_x":0,"position_y":0}},
		{"id":"two","asset_id":"b","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","params":{"width":420,"height":360,"position_x":0,"position_y":0}},
		{"id":"three","asset_id":"c","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","params":{"width":420,"height":360,"position_x":0,"position_y":0}}]`
	assets := `[
		{"asset_id":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/a.jpg"},
		{"asset_id":"b","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://example.test/b.jpg"},
		{"asset_id":"c","sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","url":"https://example.test/c.jpg"}]`
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"order","video_id":"order","width":1280,"height":720,"fps_num":24,"fps_den":1,` +
		`"items":[{"id":"trio","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in","start_ms":0,"end_ms":5000,"duration_ms":5000,` +
		`"asset_refs":` + assets + `,"image_layers":` + layers + `}]}`)
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile three-layer composite: %v", err)
	}
	var got []string
	for _, layer := range compiled.Plan.Layers {
		if layer.Asset != "" {
			got = append(got, layer.Asset)
		}
	}
	want := []string{"assets/semantic/a.jpg", "assets/semantic/b.jpg", "assets/semantic/c.jpg"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("lowered layer order = %v, want declaration order %v", got, want)
	}

	// The published cardinality must be the one the compiler validates: a
	// two-image-with-text composition whose layers carry no caption fails
	// closed against the layout's caption bounds.
	uncaptionedLayer := func(id string) string {
		return fmt.Sprintf(`{"id":%q,"asset_id":%q,"start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","params":{"width":420,"height":360,"position_x":0,"position_y":0}}`, id, id)
	}
	dense := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"dense","video_id":"dense","width":1280,"height":720,"fps_num":24,"fps_den":1,` +
		`"items":[{"id":"pair","kind":"entity_image","template_id":"image_popup","composition_id":"image_double_with_text","preset_id":"image_focus_in","start_ms":0,"end_ms":5000,"duration_ms":5000,` +
		`"asset_refs":` + assets + `,"image_layers":[` + uncaptionedLayer("a") + `,` + uncaptionedLayer("b") + `]}]}`)
	_, err = CompileSemantic(dense)
	if err == nil || !strings.Contains(err.Error(), "caption") {
		t.Fatalf("a text composition without its required caption must fail closed, got %v", err)
	}
}
