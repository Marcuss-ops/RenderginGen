package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// groupTestPlan builds a minimal entity_group plan: two captioned members on
// a 1920×1080 canvas, no explicit positions, so slots decide geometry.
func groupTestPlan(extra map[string]any, members []any) map[string]any {
	item := map[string]any{
		"id":          "duo",
		"kind":        "entity_group",
		"template_id": "ENTITY_GROUP",
		"start_ms":    0, "end_ms": 5000,
		"asset_refs": []any{
			map[string]any{"asset_id": "a", "sha256": strings.Repeat("a", 64), "url": "https://store.example/a.png", "media_type": "image/png"},
			map[string]any{"asset_id": "b", "sha256": strings.Repeat("b", 64), "url": "https://store.example/b.png", "media_type": "image/png"},
		},
		"image_layers": members,
	}
	params := map[string]any{}
	for k, v := range extra {
		params[k] = v
	}
	if len(params) > 0 {
		item["params"] = params
	}
	return map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "entity-group", "video_id": "entity-group",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"items": []any{item},
	}
}

func groupTestMembers() []any {
	return []any{
		map[string]any{"id": "a", "asset_id": "a", "start_ms": 0, "end_ms": 5000, "preset_id": "image_scale_in", "caption": "First"},
		map[string]any{"id": "b", "asset_id": "b", "start_ms": 0, "end_ms": 5000, "preset_id": "image_scale_in", "caption": "Second"},
	}
}

func compileGroupPlan(t *testing.T, plan map[string]any) CompileResult {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic entity group plan: %v", err)
	}
	return result
}

// TestEntityGroupDuoSlotsMembersSideBySide proves the group lowers two
// members to the duo slots (∓W/4) with their captions preserved.
func TestEntityGroupDuoSlotsMembersSideBySide(t *testing.T) {
	result := compileGroupPlan(t, groupTestPlan(nil, groupTestMembers()))
	var images []Layer
	var captions int
	for _, layer := range result.Plan.Layers {
		switch layer.Type {
		case "image":
			images = append(images, layer)
		case "text":
			captions++
		}
	}
	if len(images) != 2 {
		t.Fatalf("image layers = %d, want 2: %+v", len(images), result.Plan.Layers)
	}
	if captions != 2 {
		t.Fatalf("caption layers = %d, want 2", captions)
	}
	xs := map[float64]bool{}
	for _, layer := range images {
		if len(layer.Position) != 2 {
			t.Fatalf("image layer %q has no position: %+v", layer.ID, layer)
		}
		xs[layer.Position[0]] = true
		if layer.Position[1] != 0 {
			t.Errorf("image layer %q y = %v, want 0 on the duo row", layer.ID, layer.Position[1])
		}
	}
	if !xs[-480] || !xs[480] {
		t.Errorf("duo slot x positions = %v, want {-480 480}", xs)
	}
}

// TestEntityGroupUsesCertifiedCardSlots proves every group size lowers to the
// same certified 1920x1080 card table used by image-group recipes. This is the
// single slot source: a divergent second table would place the same trio,
// quad or penta differently depending on which recipe compiled it.
func TestEntityGroupUsesCertifiedCardSlots(t *testing.T) {
	all := []string{"a", "b", "c", "d", "e"}
	for count := 2; count <= 5; count++ {
		ids := all[:count]
		assets := make([]any, 0, count)
		members := make([]any, 0, count)
		for _, id := range ids {
			assets = append(assets, map[string]any{"asset_id": id, "sha256": strings.Repeat(id, 64), "url": "https://store.example/" + id + ".png", "media_type": "image/png"})
			members = append(members, map[string]any{"id": id, "asset_id": id, "start_ms": 0, "end_ms": 5000, "preset_id": "image_scale_in"})
		}
		plan := groupTestPlan(nil, members)
		item := plan["items"].([]any)[0].(map[string]any)
		item["asset_refs"] = assets
		result := compileGroupPlan(t, plan)
		for index, id := range ids {
			slot, ok := groupMemberSlot(count, index)
			if !ok {
				t.Fatalf("groupMemberSlot(%d, %d) missing", count, index)
			}
			var found bool
			for _, layer := range result.Plan.Layers {
				if layer.Type != "image" || !strings.HasSuffix(layer.ID, ":"+id+":image") {
					continue
				}
				found = true
				if len(layer.Position) != 2 || layer.Position[0] != slot.CenterX || layer.Position[1] != slot.CenterY {
					t.Errorf("group of %d member %s position = %v, want [%v %v]", count, id, layer.Position, slot.CenterX, slot.CenterY)
				}
				if len(layer.Size) != 2 || layer.Size[0] != slot.Width || layer.Size[1] != slot.Height {
					t.Errorf("group of %d member %s size = %v, want [%v %v]", count, id, layer.Size, slot.Width, slot.Height)
				}
				if layer.Fit != FitCover {
					t.Errorf("group of %d member %s fit = %q, want %q", count, id, layer.Fit, FitCover)
				}
			}
			if !found {
				t.Fatalf("group of %d has no image layer for member %s: %+v", count, id, result.Plan.Layers)
			}
		}
	}
}

// TestEntityGroupExplicitPositionWinsOverSlot proves an authored position_x
// survives the slot defaults.
func TestEntityGroupExplicitPositionWinsOverSlot(t *testing.T) {
	members := groupTestMembers()
	members[0].(map[string]any)["params"] = map[string]any{"position_x": 11.0, "position_y": 22.0}
	result := compileGroupPlan(t, groupTestPlan(nil, members))
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" && strings.HasSuffix(layer.ID, ":a:image") {
			if len(layer.Position) != 2 || layer.Position[0] != 11 || layer.Position[1] != 22 {
				t.Fatalf("explicit position = %v, want [11 22]", layer.Position)
			}
			return
		}
	}
	t.Fatalf("no image layer for member a: %+v", result.Plan.Layers)
}

// TestEntityGroupRejectsContractViolations proves the fail-closed contract:
// one member, six members, unknown layout, mismatched layout and an
// undeclared asset are all rejected.
func TestEntityGroupRejectsContractViolations(t *testing.T) {
	members := groupTestMembers()
	six := append(append([]any{}, members...), members...)
	six = append(six, members...)
	six = append(six, members...)
	cases := map[string]map[string]any{
		"one member":     groupTestPlan(nil, members[:1]),
		"six members":    groupTestPlan(nil, six),
		"unknown layout": groupTestPlan(map[string]any{"group_layout": "circle"}, members),
		"trio for duo":   groupTestPlan(map[string]any{"group_layout": "trio"}, members),
	}
	for name, plan := range cases {
		raw, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := CompileSemantic(raw); err == nil {
			t.Errorf("CompileSemantic(%s) succeeded, want fail-closed", name)
		}
	}
	ghost := groupTestMembers()
	ghost[1].(map[string]any)["asset_id"] = "ghost"
	raw, err := json.Marshal(groupTestPlan(nil, ghost))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompileSemantic(raw); err == nil {
		t.Errorf("CompileSemantic(undeclared asset) succeeded, want fail-closed")
	}
}

// TestEntityGroupAcceptsFiveMembersAtCeiling proves the 2..5 ceiling is
// inclusive: five members compile to the penta layout, the same maximum the
// PipelineGen grouping emits (maxEntityImageGroup = 5).
func TestEntityGroupAcceptsFiveMembersAtCeiling(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e"}
	assets := make([]any, 0, len(ids))
	members := make([]any, 0, len(ids))
	for _, id := range ids {
		assets = append(assets, map[string]any{"asset_id": id, "sha256": strings.Repeat(id, 64), "url": "https://store.example/" + id + ".png", "media_type": "image/png"})
		members = append(members, map[string]any{"id": id, "asset_id": id, "start_ms": 0, "end_ms": 5000, "preset_id": "image_scale_in", "caption": id})
	}
	plan := groupTestPlan(nil, members)
	plan["items"].([]any)[0].(map[string]any)["asset_refs"] = assets
	result := compileGroupPlan(t, plan)
	if len(result.Plan.Layers) < len(ids) {
		t.Fatalf("five-member group lowered %d layers, want at least %d", len(result.Plan.Layers), len(ids))
	}
}
