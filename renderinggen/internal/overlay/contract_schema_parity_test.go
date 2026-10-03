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
		{"plan.items[].map", "#/properties/items/items/properties/map", SemanticMap{}},
		{"plan.items[].map.center", "#/$defs/map_point", SemanticMapPoint{}},
		{"plan.items[].map.pins[]", "#/properties/items/items/properties/map/properties/pins/items", SemanticMapPin{}},
		{"plan.items[].map.lods[]", "#/properties/items/items/properties/map/properties/lods/items", SemanticMapLOD{}},
		{"plan.items[].map.camera_move", "#/properties/items/items/properties/map/properties/camera_move", SemanticMapCameraMove{}},
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
	for _, field := range []string{"image_preset_id", "motion_id", "motion_params", "params", "style", "image_layers", "map", "caption_motion_id"} {
		if !contractschema.Contains(itemProperties, field) {
			t.Errorf("items.%s is read by the compiler but absent from the schema", field)
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
