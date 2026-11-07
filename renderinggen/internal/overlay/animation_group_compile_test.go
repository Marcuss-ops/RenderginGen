package overlay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func animationGroupAssetRef(id string) map[string]any {
	return map[string]any{
		"asset_id":   id,
		"sha256":     strings.Repeat("a", 64),
		"url":        "assets/semantic/" + id + ".png",
		"media_type": "image/png",
	}
}

func animationGroupPhraseItem(id string) map[string]any {
	return map[string]any{
		"id": id, "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
		"preset_id": "phrase_default", "text": "Duisburg, 1948",
		"start_ms": 0, "end_ms": 5000,
	}
}

func animationGroupImageItem(id string) map[string]any {
	return map[string]any{
		"id": id, "kind": "entity_image", "template_id": "image_popup",
		"preset_id": "image_focus_in",
		"start_ms":  0, "end_ms": 5000,
		"asset_refs": []any{animationGroupAssetRef(id)},
	}
}

func animationGroupDateItem(id string) map[string]any {
	return map[string]any{
		"id": id, "kind": "number", "template_id": "TIMELINE_DATE_CARD",
		"preset_id": "phrase_default", "text": "1948",
		"date":     map[string]any{"value": "1948"},
		"start_ms": 0, "end_ms": 5000,
	}
}

func animationGroupPlan(items, groups []any) map[string]any {
	return map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "animation-group", "video_id": "animation-group",
		"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
		"duration_ms": 5000, "items": items, "animation_groups": groups,
	}
}

func compileAnimationGroupPlan(t *testing.T, plan map[string]any) CompileResult {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic animation group: %v", err)
	}
	return result
}

func animationGroupSequence(members ...any) map[string]any {
	return map[string]any{
		"id": "scene-1", "start_ms": 0, "end_ms": 4000,
		"choreography_id": AnimationGroupChoreographySequenceV1,
		"params":          map[string]any{"stagger_frames": 4},
		"members":         members,
	}
}

func animationGroupMember(itemID, role string) map[string]any {
	return map[string]any{"item_id": itemID, "role": role}
}

func animationGroupLayerHasAnimation(layer Layer) bool {
	return layer.Animation != nil && (len(layer.Animation.Tracks) > 0 || len(layer.Animation.TextAnimators) > 0) || len(layer.TextAnimators) > 0
}

func animationGroupFindLayer(result CompileResult, id string, layerType string) (Layer, bool) {
	for _, layer := range result.Plan.Layers {
		if layer.ID == id && (layerType == "" || layer.Type == layerType) {
			return layer, true
		}
	}
	return Layer{}, false
}

func animationGroupFindImageLayer(result CompileResult, prefix string) (Layer, bool) {
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" && strings.HasPrefix(layer.ID, prefix) {
			return layer, true
		}
	}
	return Layer{}, false
}

// TestAnimationGroupSequenceStaggersHeterogeneousMembers is the core V2 proof:
// one group owns a phrase, an image and a single date card, spaces their
// entrances by four frames and gives every member a lowered animation.
func TestAnimationGroupSequenceStaggersHeterogeneousMembers(t *testing.T) {
	groups := []any{animationGroupSequence(
		animationGroupMember("headline", AnimationGroupRolePhrase),
		animationGroupMember("portrait", AnimationGroupRoleImage),
		animationGroupMember("date", AnimationGroupRoleDate),
	)}
	result := compileAnimationGroupPlan(t, animationGroupPlan(
		[]any{animationGroupPhraseItem("headline"), animationGroupImageItem("portrait"), animationGroupDateItem("date")},
		groups,
	))

	headline, ok := animationGroupFindLayer(result, "headline", "text")
	if !ok {
		t.Fatalf("headline text layer missing: %+v", result.Plan.Layers)
	}
	portrait, ok := animationGroupFindImageLayer(result, "portrait")
	if !ok {
		t.Fatalf("portrait image layer missing: %+v", result.Plan.Layers)
	}
	date, ok := animationGroupFindLayer(result, "date", "text")
	if !ok {
		t.Fatalf("date text layer missing: %+v", result.Plan.Layers)
	}
	for name, layer := range map[string]Layer{"headline": headline, "portrait": portrait, "date": date} {
		if !animationGroupLayerHasAnimation(layer) {
			t.Errorf("%s has no lowered group animation: %+v", name, layer)
		}
	}
	if headline.StartFrame != 0 || portrait.StartFrame != 4 || date.StartFrame != 8 {
		t.Fatalf("group stagger start frames = %d/%d/%d, want 0/4/8", headline.StartFrame, portrait.StartFrame, date.StartFrame)
	}
	for name, layer := range map[string]Layer{"headline": headline, "portrait": portrait, "date": date} {
		if layer.StartFrame+layer.DurationFrames != 96 {
			t.Errorf("%s window ends at frame %d, want group end 96", name, layer.StartFrame+layer.DurationFrames)
		}
	}
}

// TestAnimationGroupStableSelectionIsOnePerRole proves every member of the same
// role shares one selection and that a retry of the same plan keeps it.
func TestAnimationGroupStableSelectionIsOnePerRole(t *testing.T) {
	build := func() *semanticPlan {
		return &semanticPlan{
			PlanID: "stable", VideoID: "stable", FPSNum: 24, FPSDen: 1,
			Items: []semanticItem{
				{ID: "first", Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "First", StartMS: 0, EndMS: 5000},
				{ID: "second", Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "Second", StartMS: 0, EndMS: 5000},
			},
			AnimationGroups: []semanticAnimationGroup{{
				ID: "scene", StartMS: 0, EndMS: 4000,
				Params: map[string]any{"stagger_frames": 2},
				Members: []semanticAnimationGroupMember{
					{ItemID: "first", Role: AnimationGroupRolePhrase},
					{ItemID: "second", Role: AnimationGroupRolePhrase},
				},
			}},
		}
	}
	firstPlan := build()
	if err := resolveAnimationGroups(firstPlan); err != nil {
		t.Fatalf("resolve first group: %v", err)
	}
	secondPlan := build()
	if err := resolveAnimationGroups(secondPlan); err != nil {
		t.Fatalf("resolve retry group: %v", err)
	}
	if firstPlan.Items[0].MotionID == "" || firstPlan.Items[0].MotionID != firstPlan.Items[1].MotionID {
		t.Fatalf("same-role members do not share one motion: %q vs %q", firstPlan.Items[0].MotionID, firstPlan.Items[1].MotionID)
	}
	if firstPlan.Items[0].MotionID != secondPlan.Items[0].MotionID {
		t.Fatalf("group selection is not retry-stable: %q vs %q", firstPlan.Items[0].MotionID, secondPlan.Items[0].MotionID)
	}
	if firstPlan.Items[0].StartMS != 0 || firstPlan.Items[1].StartMS != 83 {
		t.Fatalf("stagger offsets = %d/%d ms, want 0/83", firstPlan.Items[0].StartMS, firstPlan.Items[1].StartMS)
	}
}

func TestAnimationGroupExplicitMotionWinsForItsMember(t *testing.T) {
	src := &semanticPlan{
		PlanID: "explicit", VideoID: "explicit", FPSNum: 24, FPSDen: 1,
		Items: []semanticItem{
			{ID: "phrase", Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "Explicit", StartMS: 0, EndMS: 5000},
		},
		AnimationGroups: []semanticAnimationGroup{{
			ID: "scene", StartMS: 0, EndMS: 4000,
			Members: []semanticAnimationGroupMember{
				{ItemID: "phrase", Role: AnimationGroupRolePhrase, MotionID: "text_fade_up"},
				{ItemID: "phrase-2", Role: AnimationGroupRolePhrase, MotionID: "text_fade_up"},
			},
		}},
	}
	src.Items = append(src.Items, semanticItem{ID: "phrase-2", Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "Second", StartMS: 0, EndMS: 5000})
	if err := resolveAnimationGroups(src); err != nil {
		t.Fatalf("resolve explicit motions: %v", err)
	}
	for _, item := range src.Items {
		if item.MotionID != "text_fade_up" {
			t.Fatalf("item %q motion = %q, want explicit text_fade_up", item.ID, item.MotionID)
		}
	}
}

func TestAnimationGroupImageAndCaptionShareOneEntity(t *testing.T) {
	duration := int64(5000)
	group := semanticAnimationGroup{
		ID: "portrait-group", StartMS: 0, EndMS: 4000,
		Members: []semanticAnimationGroupMember{
			{ItemID: "person", Role: AnimationGroupRoleImage},
			{ItemID: "person", Role: AnimationGroupRoleCaption},
		},
	}
	src := &semanticPlan{
		PlanID: "caption", VideoID: "caption", FPSNum: 24, FPSDen: 1,
		Items: []semanticItem{{
			ID: "person", Kind: "entity_card", Template: "PERSON", PresetID: "phrase_default",
			Text: "Ada Lovelace", EntityID: "person:ada", EntityCaption: "Ada Lovelace",
			ImagePresetID: "image_scale_in", StartMS: 0, EndMS: 5000, DurationMS: &duration,
			Assets: []SemanticAssetRef{{ID: "person", SHA256: strings.Repeat("a", 64), URL: "assets/semantic/person.png", MediaType: "image/png"}},
		}},
		AnimationGroups: []semanticAnimationGroup{group},
	}
	if err := resolveAnimationGroups(src); err != nil {
		t.Fatalf("resolve image+caption group: %v", err)
	}
	item := src.Items[0]
	if item.MotionID == "" || item.CaptionMotionID == "" {
		t.Fatalf("group did not assign both owners: image=%q caption=%q", item.MotionID, item.CaptionMotionID)
	}
	if item.StartMS != 0 || item.EndMS != 4000 {
		t.Fatalf("caption group window = [%d,%d], want [0,4000]", item.StartMS, item.EndMS)
	}
}

func TestAnimationGroupRejectsOwnershipAndContractViolations(t *testing.T) {
	baseItems := func() []semanticItem {
		return []semanticItem{
			{ID: "phrase", Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "Phrase", StartMS: 0, EndMS: 5000},
			{ID: "image", Kind: "entity_image", Template: "image_popup", PresetID: "image_focus_in", StartMS: 0, EndMS: 5000,
				Assets: []SemanticAssetRef{{ID: "image", SHA256: strings.Repeat("a", 64), URL: "assets/semantic/image.png", MediaType: "image/png"}}},
		}
	}
	baseGroup := func() semanticAnimationGroup {
		return semanticAnimationGroup{
			ID: "scene", StartMS: 0, EndMS: 4000,
			Members: []semanticAnimationGroupMember{
				{ItemID: "phrase", Role: AnimationGroupRolePhrase},
				{ItemID: "image", Role: AnimationGroupRoleImage},
			},
		}
	}
	run := func(name string, mutate func(*semanticPlan)) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			src := &semanticPlan{PlanID: "reject", VideoID: "reject", FPSNum: 24, FPSDen: 1, Items: baseItems(), AnimationGroups: []semanticAnimationGroup{baseGroup()}}
			mutate(src)
			if err := resolveAnimationGroups(src); err == nil {
				t.Fatal("invalid animation group was accepted")
			}
		})
	}

	run("item already owns motion", func(src *semanticPlan) { src.Items[0].MotionID = "text_fade_up" })
	run("image owns group motion", func(src *semanticPlan) { src.Items[1].GroupMotionID = "image_group_slide_in" })
	run("image member owns a caption motion", func(src *semanticPlan) { src.Items[1].CaptionMotionID = "entity_caption_rise" })
	run("caption member owns a caption motion", func(src *semanticPlan) {
		src.Items[1].EntityCaption = "Portrait"
		src.AnimationGroups[0].Members[1].Role = AnimationGroupRoleCaption
		src.Items[1].CaptionMotionID = "entity_caption_rise"
	})
	run("item split across two groups", func(src *semanticPlan) {
		src.Items = append(src.Items, semanticItem{ID: "extra", Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "Extra", StartMS: 0, EndMS: 5000})
		src.AnimationGroups = append(src.AnimationGroups, semanticAnimationGroup{
			ID: "scene-2", StartMS: 0, EndMS: 4000,
			Members: []semanticAnimationGroupMember{
				{ItemID: "image", Role: AnimationGroupRoleImage},
				{ItemID: "extra", Role: AnimationGroupRolePhrase},
			},
		})
	})
	run("duplicate group id", func(src *semanticPlan) {
		src.AnimationGroups = append(src.AnimationGroups, baseGroup())
	})
	run("duplicate member role", func(src *semanticPlan) {
		src.AnimationGroups[0].Members = append(src.AnimationGroups[0].Members, semanticAnimationGroupMember{ItemID: "phrase", Role: AnimationGroupRolePhrase})
	})
	run("unknown item", func(src *semanticPlan) { src.AnimationGroups[0].Members[0].ItemID = "ghost" })
	run("unsupported role", func(src *semanticPlan) { src.AnimationGroups[0].Members[0].Role = "camera" })
	run("wrong role for kind", func(src *semanticPlan) { src.AnimationGroups[0].Members[0].Role = AnimationGroupRoleImage })
	run("motion for wrong target", func(src *semanticPlan) { src.AnimationGroups[0].Members[0].MotionID = "image_focus_reveal" })
	run("params without motion", func(src *semanticPlan) {
		src.AnimationGroups[0].Members[0].MotionParams = map[string]any{"enter_frames": 2}
	})
	run("one member", func(src *semanticPlan) { src.AnimationGroups[0].Members = src.AnimationGroups[0].Members[:1] })
	run("nine members", func(src *semanticPlan) {
		for index := 0; index < 7; index++ {
			id := "extra-" + string(rune('a'+index))
			src.Items = append(src.Items, semanticItem{ID: id, Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "Extra", StartMS: 0, EndMS: 5000})
			src.AnimationGroups[0].Members = append(src.AnimationGroups[0].Members, semanticAnimationGroupMember{ItemID: id, Role: AnimationGroupRolePhrase})
		}
	})
	run("unknown params", func(src *semanticPlan) { src.AnimationGroups[0].Params = map[string]any{"curve": "ease"} })
	run("negative stagger", func(src *semanticPlan) { src.AnimationGroups[0].Params = map[string]any{"stagger_frames": -1} })
	run("fractional stagger", func(src *semanticPlan) { src.AnimationGroups[0].Params = map[string]any{"stagger_frames": 1.5} })
	run("offset outside group", func(src *semanticPlan) {
		offset := int64(5000)
		src.AnimationGroups[0].Members[0].OffsetMS = &offset
	})
	run("end before start", func(src *semanticPlan) {
		offset := int64(1000)
		end := int64(1000)
		src.AnimationGroups[0].Members[0].OffsetMS = &offset
		src.AnimationGroups[0].Members[0].EndOffsetMS = &end
	})
	run("caption without caption", func(src *semanticPlan) {
		src.AnimationGroups[0].Members[0].Role = AnimationGroupRoleCaption
	})
}

func TestAnimationGroupLegacyPlanWithoutGroupsStillCompiles(t *testing.T) {
	plan := animationGroupPlan([]any{animationGroupPhraseItem("headline")}, nil)
	delete(plan, "animation_groups")
	result := compileAnimationGroupPlan(t, plan)
	headline, ok := animationGroupFindLayer(result, "headline", "text")
	if !ok || !animationGroupLayerHasAnimation(headline) {
		t.Fatalf("legacy phrase plan lost its existing animation: %+v", result.Plan.Layers)
	}
}

func TestRuntimeAnimationGroupCatalogPublishesRolePools(t *testing.T) {
	catalog := CompiledRuntimeAnimationCatalog()
	if len(catalog.GroupChoreographies) != 1 {
		t.Fatalf("group choreographies = %d, want 1", len(catalog.GroupChoreographies))
	}
	group := catalog.GroupChoreographies[0]
	if group.ID != AnimationGroupChoreographySequenceV1 || group.Owner != "animation_group" ||
		group.Selection != "stable_random_per_role" || group.MinimumMembers != 2 || group.MaximumMembers != 8 {
		t.Fatalf("group choreography metadata = %+v", group)
	}
	if len(group.Roles) != 5 {
		t.Fatalf("group roles = %d, want 5", len(group.Roles))
	}
	for _, role := range group.Roles {
		if role.Role == "" || role.MotionTarget == "" || role.MotionCount == 0 || len(role.MotionIDs) != role.MotionCount {
			t.Errorf("role pool is incomplete: %+v", role)
		}
	}
	var encoded strings.Builder
	if err := WriteRuntimeAnimationCatalog(&encoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded.String(), `"group_choreographies"`) || !strings.Contains(encoded.String(), AnimationGroupChoreographySequenceV1) {
		t.Fatal("compiled catalog does not publish the group choreography")
	}
}

// TestAnimationGroupDerives3DMetadataFromTracks pins the V2 metadata rule: the
// group validates 3D/camera truth from the selected motion's actual tracks, not
// from a second authored flag that could drift. A motion that claims 3D without
// any camera-backed track is rejected instead of silently enabling a camera.
func TestAnimationGroupDerives3DMetadataFromTracks(t *testing.T) {
	registry := motion.Registry
	motion.Registry = motion.NewRegistry()
	defer func() { motion.Registry = registry }()
	requires3D, requiresCamera := true, true
	if err := motion.Registry.Register("group_bad_3d", motion.DeclarativePlugin{Definition: motion.MotionDefinition{
		ID: "group_bad_3d", Category: "generic_motion_v1", Targets: []string{"important_phrase", "text"},
		Requires3D: &requires3D, RequiresCamera: &requiresCamera,
		Tracks: []motion.TrackDefinition{{
			Property: "opacity",
			Keyframes: []motion.AnimationKeyframe{
				{Frame: 0, Value: 0.0},
				{Frame: 12, Value: 1.0},
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	src := &semanticPlan{
		PlanID: "metadata", VideoID: "metadata", FPSNum: 24, FPSDen: 1,
		Items: []semanticItem{
			{ID: "one", Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "One", StartMS: 0, EndMS: 5000},
			{ID: "two", Kind: "important_phrase", Template: "IMPORTANT_PHRASE", PresetID: "phrase_default", Text: "Two", StartMS: 0, EndMS: 5000},
		},
		AnimationGroups: []semanticAnimationGroup{{
			ID: "scene", StartMS: 0, EndMS: 4000,
			Members: []semanticAnimationGroupMember{
				{ItemID: "one", Role: AnimationGroupRolePhrase, MotionID: "group_bad_3d"},
				{ItemID: "two", Role: AnimationGroupRolePhrase, MotionID: "group_bad_3d"},
			},
		}},
	}
	if err := resolveAnimationGroups(src); err == nil || !strings.Contains(err.Error(), "requires_3d") {
		t.Fatalf("invalid 3D metadata should fail closed, got %v", err)
	}
}

func TestAnimationGroupContractDecodesAndRejectsUnknownMemberFields(t *testing.T) {
	plan := animationGroupPlan(
		[]any{animationGroupPhraseItem("headline"), animationGroupImageItem("portrait")},
		[]any{animationGroupSequence(
			animationGroupMember("headline", AnimationGroupRolePhrase),
			animationGroupMember("portrait", AnimationGroupRoleImage),
		)},
	)
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	src, err := decodeSemanticPlanStrict(string(raw))
	if err != nil {
		t.Fatalf("group plan must decode: %v", err)
	}
	if len(src.AnimationGroups) != 1 || len(src.AnimationGroups[0].Members) != 2 {
		t.Fatalf("group plan decoded to %+v", src.AnimationGroups)
	}
	plan["animation_groups"].([]any)[0].(map[string]any)["members"].([]any)[0].(map[string]any)["unexpected"] = true
	raw, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSemanticPlanStrict(string(raw)); err == nil {
		t.Fatal("unknown member field must be rejected by the strict decoder")
	}
}
