package overlay

import (
	"fmt"
	"testing"
)

// TestGroupMemberSlotsMatchCertifiedShowcaseGeometry pins the shared slot
// tables to the ChrononTemplate multiple_images showcase suites: any drift
// breaks the no-op guarantee for certified renders.
func TestGroupMemberSlotsMatchCertifiedShowcaseGeometry(t *testing.T) {
	want := map[int][]ImageSlot{
		2: {
			{CenterX: -480, CenterY: 0, Width: 620, Height: 720},
			{CenterX: 480, CenterY: 0, Width: 620, Height: 720},
		},
		3: {
			{CenterX: -540, CenterY: 0, Width: 500, Height: 680},
			{CenterX: 0, CenterY: 0, Width: 500, Height: 680},
			{CenterX: 540, CenterY: 0, Width: 500, Height: 680},
		},
		4: {
			{CenterX: -360, CenterY: -225, Width: 540, Height: 400},
			{CenterX: 360, CenterY: -225, Width: 540, Height: 400},
			{CenterX: -360, CenterY: 225, Width: 540, Height: 400},
			{CenterX: 360, CenterY: 225, Width: 540, Height: 400},
		},
		5: {
			{CenterX: -680, CenterY: 0, Width: 320, Height: 580},
			{CenterX: -340, CenterY: 0, Width: 320, Height: 580},
			{CenterX: 0, CenterY: 0, Width: 320, Height: 580},
			{CenterX: 340, CenterY: 0, Width: 320, Height: 580},
			{CenterX: 680, CenterY: 0, Width: 320, Height: 580},
		},
	}
	for count, slots := range want {
		for index, expected := range slots {
			got, ok := groupMemberSlot(count, index)
			if !ok {
				t.Fatalf("count %d index %d: no slot", count, index)
			}
			if got != expected {
				t.Errorf("count %d index %d = %+v, want %+v", count, index, got, expected)
			}
		}
	}
	for _, count := range []int{0, 1, 6, -2} {
		if _, ok := groupMemberSlot(count, 0); ok {
			t.Errorf("count %d must fail closed, got a slot", count)
		}
	}
	if _, ok := groupMemberSlot(2, 2); ok {
		t.Error("duo index 2 must fail closed")
	}
	if _, err := autoFitGroupMember(&semanticPlan{Width: 1920, Height: 1080}, &Layer{}, 6, 0); err == nil {
		t.Error("unsupported group count must fail closed")
	}
}

// TestAutoFitLayerToSlotScalesWithCanvas proves the same design slot lands
// proportionally on any canvas and is a no-op once applied.
func TestAutoFitLayerToSlotScalesWithCanvas(t *testing.T) {
	src := &semanticPlan{Width: 1280, Height: 720}
	layer := &Layer{Size: []float64{1280, 720}, Position: []float64{0, 0}, Fit: FitContain}
	slot, ok := groupMemberSlot(2, 0)
	if !ok {
		t.Fatal("no duo slot")
	}
	if !autoFitLayerToSlot(src, layer, slot) {
		t.Fatal("landscape layer was not fitted")
	}
	// 1280/1920 = 720/1080 = 2/3: card 620x720 -> 413.33x480 at x=-320.
	if got := layer.Size[0]; got < 413 || got > 414 {
		t.Errorf("fitted width = %v, want ~413.33", got)
	}
	if layer.Size[1] != 480 {
		t.Errorf("fitted height = %v, want 480", layer.Size[1])
	}
	if layer.Position[0] != -320 || layer.Position[1] != 0 {
		t.Errorf("fitted position = %v, want [-320 0]", layer.Position)
	}
	if layer.Fit != FitCover {
		t.Errorf("fitted fit = %q, want cover", layer.Fit)
	}
	if autoFitLayerToSlot(src, layer, slot) {
		t.Error("second fit must be a no-op")
	}
}

// TestAutoFitEntityPortraitCoversLandscape keeps vertical entity cards
// untouched and covers anything else into the supported portrait.
func TestAutoFitEntityPortraitCoversLandscape(t *testing.T) {
	vertical := &Layer{Size: []float64{600, 800}, Fit: FitContain}
	if autoFitEntityPortrait(vertical) {
		t.Error("vertical entity image must pass through untouched")
	}
	if vertical.Fit != FitContain {
		t.Error("vertical entity fit changed")
	}
	landscape := &Layer{Size: []float64{1000, 600}, Position: []float64{-480, 0}, Fit: FitContain}
	if !autoFitEntityPortrait(landscape) {
		t.Fatal("landscape entity image was not fitted")
	}
	if landscape.Size[0] != 450 || landscape.Size[1] != 600 {
		t.Errorf("fitted entity size = %v, want [450 600]", landscape.Size)
	}
	if landscape.Position[0] != -480 || landscape.Fit != FitCover {
		t.Errorf("entity center/fit changed: %+v", landscape)
	}
	if autoFitEntityPortrait(nil) || autoFitEntityPortrait(&Layer{}) {
		t.Error("nil/empty layer must be a no-op")
	}
}

func duoSoftGroupPlan(motionID string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"duo-soft-%s","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"duo","kind":"image","template_id":"image_popup","group_motion_id":%q,"start_ms":0,"end_ms":5000,"asset_refs":[{"asset_id":"left","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/left.png"},{"asset_id":"right","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://example.test/right.png"}],"image_layers":[{"id":"left","asset_id":"left","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"},{"id":"right","asset_id":"right","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"}]}]}`,
		motionID, motionID))
}

// TestDuoSoftMotionsCompileToSideBySideCards is the runtime proof: two
// nearby (landscape-preset) images compiled with each new soft choreography
// land on the supported duo cards, side by side, with cover fit.
func TestDuoSoftMotionsCompileToSideBySideCards(t *testing.T) {
	ids := []string{
		"duo_soft_bloom_rise",
		"duo_soft_drift_settle",
		"duo_soft_cross_bloom",
		"duo_soft_breathe_hold",
		"duo_soft_stagger_glow",
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			if !animationUsesGroupRecipe(id) {
				t.Fatalf("%q is not a certified multi-image motion", id)
			}
			result, err := CompileSemantic(duoSoftGroupPlan(id))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			var images []Layer
			for _, layer := range result.Plan.Layers {
				if layer.Type == "image" {
					images = append(images, layer)
				}
			}
			if len(images) != 2 {
				t.Fatalf("got %d image layers, want 2: %+v", len(images), result.Plan.Layers)
			}
			wantPositions := [][]float64{{-480, 0}, {480, 0}}
			for i, image := range images {
				if len(image.Size) != 2 || image.Size[0] != 620 || image.Size[1] != 720 {
					t.Errorf("member %d size = %v, want [620 720]", i, image.Size)
				}
				if len(image.Position) != 2 || image.Position[0] != wantPositions[i][0] || image.Position[1] != 0 {
					t.Errorf("member %d position = %v, want %v", i, image.Position, wantPositions[i])
				}
				if image.Fit != FitCover {
					t.Errorf("member %d fit = %q, want cover", i, image.Fit)
				}
				if image.Animation == nil || len(image.Animation.Tracks) == 0 {
					t.Errorf("member %d lost its choreography tracks", i)
				}
			}
			if images[0].Position[0] >= images[1].Position[0] {
				t.Error("members are not ordered left/right")
			}
		})
	}
}
