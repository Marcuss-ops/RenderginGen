package overlay

import (
	"encoding/json"
	"testing"
)

func TestRenderPlanWireVersionUsesOneFeatureSelector(t *testing.T) {
	cases := []struct {
		name    string
		plan    *Plan
		schema  string
		version int
	}{
		{name: "plain plan", plan: &Plan{}, schema: RenderPlanSchemaV2, version: RenderPlanVersionV2},
		{name: "camera without layers remains v2", plan: &Plan{Camera: &CameraPlan{}}, schema: RenderPlanSchemaV2, version: RenderPlanVersionV2},
		{name: "camera animation requires v3", plan: &Plan{CameraAnimation: &CameraAnimation{}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "camera with layer", plan: &Plan{Camera: &CameraPlan{}, Layers: []Layer{{ID: "image"}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "shape", plan: &Plan{Layers: []Layer{{Type: "shape"}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "frame stroke", plan: &Plan{Layers: []Layer{{FrameStroke: &LayerStroke{Width: 2}}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "screen space", plan: &Plan{Layers: []Layer{{ScreenSpace: true}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "effects", plan: &Plan{Layers: []Layer{{Effects: []LayerEffect{{Type: "glow"}}}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "effect tracks", plan: &Plan{Layers: []Layer{{EffectParamTracks: []LayerEffectParamTrack{{EffectID: "glow", Param: "intensity"}}}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "parent", plan: &Plan{Layers: []Layer{{Parent: "parent"}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "transition", plan: &Plan{Layers: []Layer{{TransitionIn: &LayerTransition{ID: "fade"}}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "mask", plan: &Plan{Layers: []Layer{{Masks: []LayerMask{{Type: "rect"}}}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
		{name: "animation property", plan: &Plan{Layers: []Layer{{Animation: &LayerAnimation{Tracks: []AnimationTrack{{Property: "blur"}}}}}}, schema: RenderPlanSchemaV3, version: RenderPlanVersionV3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSchema, gotVersion := RenderPlanWireVersion(tc.plan)
			if gotSchema != tc.schema || gotVersion != tc.version {
				t.Fatalf("RenderPlanWireVersion = %s v%d, want %s v%d", gotSchema, gotVersion, tc.schema, tc.version)
			}
			wire, err := tc.plan.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var decoded struct {
				Schema  string `json:"schema"`
				Version int    `json:"version"`
			}
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatalf("decode marshaled plan: %v", err)
			}
			if decoded.Schema != tc.schema || decoded.Version != tc.version {
				t.Fatalf("wire identity = %s v%d, want %s v%d", decoded.Schema, decoded.Version, tc.schema, tc.version)
			}
			if tc.plan.Schema != "" || tc.plan.Version != 0 {
				t.Fatalf("Marshal mutated the caller's plan identity: %s v%d", tc.plan.Schema, tc.plan.Version)
			}
			indented, err := tc.plan.MarshalIndent("", "  ")
			if err != nil {
				t.Fatalf("MarshalIndent: %v", err)
			}
			var indentedIdentity struct {
				Schema  string `json:"schema"`
				Version int    `json:"version"`
			}
			if err := json.Unmarshal(indented, &indentedIdentity); err != nil {
				t.Fatalf("decode indented plan: %v", err)
			}
			if indentedIdentity != decoded {
				t.Fatalf("MarshalIndent identity = %+v, Marshal identity = %+v", indentedIdentity, decoded)
			}
		})
	}
}

func TestMarshalNilRenderPlanReturnsError(t *testing.T) {
	var plan *Plan
	if _, err := plan.Marshal(); err == nil {
		t.Fatal("Marshal on nil plan must return an error")
	}
	if _, err := plan.MarshalIndent("", "  "); err == nil {
		t.Fatal("MarshalIndent on nil plan must return an error")
	}
}
