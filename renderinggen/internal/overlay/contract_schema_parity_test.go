package overlay

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/contractschema"
)

const overlayContractSchema = "overlay-plan.v1.schema.json"

func fieldByName(typ reflect.Type, jsonName string) string {
	for i := 0; i < typ.NumField(); i++ {
		if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] == jsonName {
			return jsonName
		}
	}
	return ""
}

// TestContractSchemaMatchesCompilerStructs is the bidirectional pin. Every
// schema object the compiler decodes is compared with its Go mirror.
func TestContractSchemaMatchesCompilerStructs(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	cases := []struct {
		name, pointer string
		goValue       any
	}{
		{"plan", "#/", semanticPlan{}},
		{"plan.animation_policies[]", "#/$defs/animation_policy", semanticAnimationPolicy{}},
		{"plan.source", "#/properties/source", semanticSource{}},
		{"plan.source_frame", "#/properties/source_frame", semanticSourceFrame{}},
		{"plan.source_frame.border", "#/properties/source_frame/properties/border", semanticFrameBorder{}},
		{"plan.source_frame.shadow", "#/properties/source_frame/properties/shadow", semanticFrameShadow{}},
		{"plan.source_frame.stroke", "#/properties/source_frame/properties/stroke", semanticFrameStroke{}},
		{"plan.background", "#/properties/background", semanticBackground{}},
		{"plan.subtitles", "#/properties/subtitles", semanticSubtitles{}},
		{"plan.watermark", "#/properties/watermark", semanticWatermark{}},
		{"plan.audio", "#/properties/audio", semanticAudio{}},
		{"plan.items[]", "#/properties/items/items", semanticItem{}},
		{"plan.items[].asset_refs[]", "#/properties/items/items/properties/asset_refs/items", SemanticAssetRef{}},
		{"plan.items[].image_layers[]", "#/properties/items/items/properties/image_layers/items", SemanticImageLayer{}},
		{"plan.items[].frame", "#/properties/items/items/properties/frame", semanticSourceFrame{}},
		{"plan.items[].frame.border", "#/properties/items/items/properties/frame/properties/border", semanticFrameBorder{}},
		{"plan.items[].frame.shadow", "#/properties/items/items/properties/frame/properties/shadow", semanticFrameShadow{}},
		{"plan.items[].frame.stroke", "#/properties/items/items/properties/frame/properties/stroke", semanticFrameStroke{}},
		{"plan.items[].metric", "#/$defs/metric_data", SemanticMetricData{}},
		{"plan.items[].date", "#/$defs/date_data", SemanticDateData{}},
		{"plan.items[].map", "#/properties/items/items/properties/map", SemanticMap{}},
		{"plan.items[].map.center", "#/$defs/map_point", SemanticMapPoint{}},
		{"plan.items[].map.pins[]", "#/properties/items/items/properties/map/properties/pins/items", SemanticMapPin{}},
		{"plan.items[].map.pins[].label_style", "#/$defs/map_text_style", SemanticMapTextStyle{}},
		{"plan.items[].map.pins[].label_style.stroke", "#/$defs/map_text_style/properties/stroke", semanticMapTextStroke{}},
		{"plan.items[].map.pins[].label_style.shadow", "#/$defs/map_text_style/properties/shadow", semanticMapTextShadow{}},
		{"plan.items[].map.pins[].label_style.glow", "#/$defs/map_text_style/properties/glow", semanticMapTextGlow{}},
		{"plan.items[].map.pins[].label_style.background", "#/$defs/map_text_style/properties/background", semanticMapTextPlate{}},
		{"plan.items[].map.lods[]", "#/properties/items/items/properties/map/properties/lods/items", SemanticMapLOD{}},
		{"plan.items[].map.camera_move", "#/properties/items/items/properties/map/properties/camera_move", SemanticMapCameraMove{}},
		{"plan.items[].map.fly_to_feature", "#/properties/items/items/properties/map/properties/fly_to_feature", SemanticMapFlyToFeature{}},
		{"plan.items[].map.routes[]", "#/properties/items/items/properties/map/properties/routes/items", SemanticMapRoute{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := contractschema.PropertyNames(t, schema, tc.pointer)
			got := contractschema.JSONFieldNames(tc.goValue)
			if missing := contractschema.Difference(want, got); len(missing) > 0 {
				t.Errorf("schema declares %v, but Go does not decode them", missing)
			}
			if extra := contractschema.Difference(got, want); len(extra) > 0 {
				t.Errorf("Go decodes %v, but schema forbids them", extra)
			}
		})
	}
}

// TestDataBlockSchemaRequiresTheDeclaredFields pins the two required lists the
// compiler enforces. The Go validator and the schema must agree on what a
// declared block is missing, otherwise a producer would pass validation here
// and be rejected there (or the reverse) with no shared diagnostic.
func TestDataBlockSchemaRequiresTheDeclaredFields(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	cases := []struct {
		def      string
		required []string
	}{
		{"#/$defs/metric_data", []string{"value", "unit"}},
		{"#/$defs/date_data", []string{"value"}},
	}
	for _, tc := range cases {
		block := contractschema.At(t, schema, tc.def)
		required, ok := block["required"].([]any)
		if !ok {
			t.Fatalf("%s declares no required field list", tc.def)
		}
		declared := make(map[string]bool, len(required))
		for _, value := range required {
			if field, ok := value.(string); ok {
				declared[field] = true
			}
		}
		for _, field := range tc.required {
			if !declared[field] {
				t.Errorf("%s does not require compiler input %q", tc.def, field)
			}
		}
	}
	if !contractschema.Contains(contractschema.PropertyNames(t, schema, "#/properties/items/items"), "metric") ||
		!contractschema.Contains(contractschema.PropertyNames(t, schema, "#/properties/items/items"), "date") {
		t.Error("item schema does not declare the metric/date blocks the compiler decodes")
	}
}

func TestImageLayerSchemaRequiresCompilerInputs(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	imageLayer := contractschema.At(t, schema, "#/properties/items/items/properties/image_layers/items")
	required, ok := imageLayer["required"].([]any)
	if !ok {
		t.Fatal("image layer schema has no required field list")
	}
	requiredFields := make(map[string]bool, len(required))
	for _, value := range required {
		if field, ok := value.(string); ok {
			requiredFields[field] = true
		}
	}
	for _, field := range []string{"id", "asset_id", "start_ms", "end_ms", "preset_id"} {
		if !requiredFields[field] {
			t.Errorf("image layer schema does not require compiler input %q", field)
		}
	}
}

// decodeSemanticPlanStrict mirrors compileSemantic's decode step exactly: the
// same decoder and the same DisallowUnknownFields, so a field the compiler
// would reject cannot pass this test either.
func decodeSemanticPlanStrict(raw string) (semanticPlan, error) {
	var src semanticPlan
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&src)
	return src, err
}

// TestSemanticMapDecodesProducerShapeRejectsCenterExtras pins the worker side of
// the map contract against the shape PipelineGen actually emits: the ten
// declared fields decode, and a center carrying anything beyond
// {latitude, longitude} is rejected. display_name is the concrete regression:
// the producer's geodesy enrichment point used to serialize it, and the strict
// decoder would then fail the whole plan rather than merely drop the label.
func TestShapeContractDeclaresClosedRuntimeBounds(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	params := contractschema.At(t, schema, "#/properties/items/items/properties/params")
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("item.params does not declare shape runtime fields")
	}
	for _, field := range []string{"shape", "width", "height", "fill", "fill_gradient", "stroke", "radius", "corner_radius", "points", "inner_radius_ratio", "grid_spacing", "grid_line_width", "dot_radius", "arc_start_deg", "arc_sweep_deg", "arc_thickness", "world_map", "device_frame_kind", "path", "effects", "opacity"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("item.params.%s missing from shape contract", field)
		}
	}
	bounds := map[string]struct{ minimum, maximum float64 }{
		"points": {3, 64}, "grid_spacing": {4, 512}, "grid_line_width": {0.25, 32}, "dot_radius": {0.5, 64},
	}
	for field, want := range bounds {
		definition := properties[field].(map[string]any)
		if definition["minimum"] != want.minimum || definition["maximum"] != want.maximum {
			t.Errorf("%s bounds = [%v,%v], want [%v,%v]", field, definition["minimum"], definition["maximum"], want.minimum, want.maximum)
		}
	}
}

func TestSemanticMapDecodesProducerShapeRejectsCenterExtras(t *testing.T) {
	buildPlan := func(center string) string {
		return `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v",` +
			`"width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[` +
			`{"id":"scene-1-map","kind":"map","template_id":"MAP","start_ms":0,"end_ms":3000,` +
			`"asset_refs":[{"asset_id":"basemap","sha256":"` + strings.Repeat("a", 64) + `","url":"assets/maps/plate.png","media_type":"image/png"}],` +
			`"map":{"provider":"local","source_id":"plate","source_license":"operator-supplied",` +
			`"center":{` + center + `},"zoom":6,"width":1280,"height":720,` +
			`"attribution":"\u00a9 OpenStreetMap contributors","motion_id":"image_fade_reveal",` +
			`"pins":[{"id":"rome","label":"Rome","latitude":41.9028,"longitude":12.4964,"color":"#FF0000","radius_px":8}]}}]}`
	}

	src, err := decodeSemanticPlanStrict(buildPlan(`"latitude":41.9028,"longitude":12.4964`))
	if err != nil {
		t.Fatalf("producer-shaped map plan must decode: %v", err)
	}
	if len(src.Items) != 1 || src.Items[0].Map == nil {
		t.Fatalf("map declaration did not decode: %+v", src.Items)
	}
	mapItem := src.Items[0].Map
	if mapItem.Center.Latitude != 41.9028 || mapItem.Zoom != 6 || len(mapItem.Pins) != 1 || mapItem.Pins[0].ID != "rome" {
		t.Fatalf("map decoded to unexpected values: %+v", mapItem)
	}

	if _, err := decodeSemanticPlanStrict(buildPlan(`"latitude":41.9028,"longitude":12.4964,"display_name":"Rome"`)); err == nil {
		t.Fatal("center.display_name must be rejected by strict decoding")
	}
}

func TestContractSchemaDeclaresMapVisibleTextBounds(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	attribution := contractschema.At(t, schema, "#/properties/items/items/properties/map/properties/attribution")
	if attribution["maxLength"] != float64(512) {
		t.Errorf("map.attribution maxLength = %v, want 512", attribution["maxLength"])
	}
	label := contractschema.At(t, schema, "#/properties/items/items/properties/map/properties/pins/items/properties/label")
	if label["maxLength"] != float64(256) {
		t.Errorf("map.pins[].label maxLength = %v, want 256", label["maxLength"])
	}
	for _, field := range []string{"label_style", "label_offset_px", "label_priority"} {
		if !contractschema.Contains(contractschema.PropertyNames(t, schema, "#/properties/items/items/properties/map/properties/pins/items"), field) {
			t.Errorf("map.pins[].%s missing from the published schema", field)
		}
	}
}

func TestMapLabelStyleAndPlacementContractCompilesAndRemainsOptional(t *testing.T) {
	styled := georeferencedMapPlan(0, 0)
	item := styled["items"].([]any)[0].(map[string]any)
	pin := item["map"].(map[string]any)["pins"].([]any)[0].(map[string]any)
	pin["label_style"] = map[string]any{
		"font_family": "inter", "font_size_px": 28.0, "fill": "#F7F4EA",
		"stroke":     map[string]any{"color": "#07121A", "width": 2.0},
		"shadow":     map[string]any{"color": "#000000", "opacity": 0.7, "blur": 8.0, "offset": []any{1.0, 3.0}},
		"glow":       map[string]any{"color": "#68E1FD", "radius": 24.0, "intensity": 0.3},
		"background": map[string]any{"color": "#07121A", "opacity": 0.7, "radius": 8.0, "padding": []any{10.0, 6.0}},
	}
	pin["label_offset_px"] = []any{8.0, 4.0}
	pin["label_priority"] = 10
	result, err := compileMapTestPlan(t, styled)
	if err != nil {
		t.Fatalf("compile styled map label: %v", err)
	}
	label := result.Plan.Layers[2]
	if label.Style == nil || label.Style.Font != "assets/fonts/Inter.ttf" || label.Style.FontSize != 28 ||
		label.Style.Fill != "#F7F4EA" || label.Style.Stroke == nil || label.Style.Stroke.Width != 2 ||
		label.Style.Shadow == nil || label.Style.Shadow.Blur != 8 || label.Style.Glow == nil ||
		label.Style.Glow.Color != "#68E1FD" || label.Style.Background == nil || label.Style.Background.Radius != 8 {
		t.Fatalf("map text effects did not lower to the label's renderer style: %+v", label.Style)
	}
	if label.Position[0] < 0 || label.Position[1] < 0 {
		t.Fatalf("preferred label offset produced a clipped text origin: got %v", label.Position)
	}

	legacy, err := compileMapTestPlan(t, georeferencedMapPlan(0, 0))
	if err != nil {
		t.Fatalf("legacy map plan without optional style fields must remain valid: %v", err)
	}
	legacyLabel := legacy.Plan.Layers[2]
	if legacyLabel.Style == nil || legacyLabel.Style.Shadow == nil || legacyLabel.Style.Glow != nil || legacyLabel.Style.Stroke != nil {
		t.Fatalf("legacy default style was not retained: %+v", legacyLabel.Style)
	}
}

func TestMapLabelsFailClosedOnInvalidStyleOrUnresolvableCollisions(t *testing.T) {
	plan := georeferencedMapPlan(0, 0)
	pin := plan["items"].([]any)[0].(map[string]any)["map"].(map[string]any)["pins"].([]any)[0].(map[string]any)
	pin["label_style"] = map[string]any{"glow": map[string]any{"color": "#00FFFF", "radius": 24.0, "intensity": 2.0}}
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "label_style.glow") {
		t.Fatalf("out-of-contract glow intensity should fail closed, got %v", err)
	}

	plan = georeferencedMapPlan(0, 0)
	mapItem := plan["items"].([]any)[0].(map[string]any)
	mapSpec := mapItem["map"].(map[string]any)
	mapSpec["pins"] = []any{
		map[string]any{"id": "one", "label": "An extremely long city label that cannot fit without colliding", "latitude": 0.0, "longitude": 0.0, "color": "#E11D48", "radius_px": 12.0},
		map[string]any{"id": "two", "label": "Another extremely long city label that cannot fit without colliding", "latitude": 0.0, "longitude": 0.001, "color": "#E11D48", "radius_px": 12.0},
	}
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "cannot be placed") {
		t.Fatalf("unresolvable colliding labels should fail closed, got %v", err)
	}
}

func TestContractSchemaRejectsUnknownCompilerFields(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"background":{"kind":"color","color":[0,0,0,1]},"definitely_not_in_the_contract":true}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("unknown top-level field must be rejected")
	}
	itemRaw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[{"id":"i","template_id":"PERSON","preset_id":"phrase_default","text":"Ada","start_ms":0,"end_ms":1000,"unknown_item_field":1}]}`)
	if _, err := CompileSemantic(itemRaw); err == nil {
		t.Fatal("unknown item field must be rejected")
	}
}

func TestContractDeclaresEveryLiveLoweringInput(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	itemProperties := contractschema.PropertyNames(t, schema, "#/properties/items/items")
	watermarkProperties := contractschema.PropertyNames(t, schema, "#/properties/watermark")
	for _, field := range []string{"image_preset_id", "motion_id", "motion_params", "params", "style", "image_layers", "map", "caption_motion_id", "caption_motion_params", "group_id", "subgroup_id"} {
		if !contractschema.Contains(itemProperties, field) {
			t.Errorf("items.%s is read by the compiler but absent from the schema", field)
		}
	}
	imageLayerProperties := contractschema.PropertyNames(t, schema, "#/properties/items/items/properties/image_layers/items")
	for _, field := range []string{"caption_motion_id", "caption_motion_params", "group_id", "subgroup_id"} {
		if !contractschema.Contains(imageLayerProperties, field) {
			t.Errorf("image_layers[].%s is consumed by the compiler but absent from the schema", field)
		}
	}
	for _, field := range []string{"font_ref", "margin_px"} {
		if !contractschema.Contains(watermarkProperties, field) {
			t.Errorf("watermark.%s is read by the compiler but absent from the schema", field)
		}
	}
}

func TestPerChannelStyleProfileIsNotPartOfTheOverlayContract(t *testing.T) {
	schema := contractschema.Load(t, overlayContractSchema)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("overlay-plan schema has no properties")
	}
	if _, ok := properties["style_profile"]; ok {
		t.Fatal("overlay-plan must not carry per-channel style_profile")
	}
	if fieldByName(reflect.TypeOf(semanticPlan{}), "style_profile") != "" {
		t.Fatal("semanticPlan must not retain the retired style_profile field")
	}
}
