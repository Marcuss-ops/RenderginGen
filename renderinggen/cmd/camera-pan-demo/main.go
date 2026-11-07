// Command camera-pan-demo compiles the eight semantic overlays used to preview
// the reusable image-group camera-pan family: right, left, down and up, each
// with one image and with a three-image strip. It writes Chronon render plans;
// the caller renders them with chronon3d_cli.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

const outputSlug = "out/camera_pan_premium_20261106"

type clip struct {
	name       string
	mode       string
	motionID   string
	axis       string
	durationMS int
	item       map[string]any
}

func main() {
	out := flag.String("out", "", "directory for the compiled Chronon plans")
	assetsRoot := flag.String("assets-root", "", "root the plan asset URLs are relative to")
	flag.Parse()
	if *out == "" || *assetsRoot == "" {
		fmt.Fprintln(os.Stderr, "camera-pan-demo: -out and -assets-root are required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}

	var clips []clip
	for _, direction := range overlay.CameraPanDirections() {
		name := strings.TrimPrefix(direction.ID, "image_group_camera_pan_")
		if name == direction.ID || name == "" {
			fail(fmt.Errorf("unexpected camera-pan preview id %q", direction.ID))
		}
		clips = append(clips,
			clip{
				name:       "multiple_images_camera_pan_" + name,
				mode:       "multiple_images",
				motionID:   direction.ID,
				axis:       direction.Axis,
				durationMS: 10000,
				item:       stripItem(*assetsRoot, direction.ID, 10000),
			},
			clip{
				name:       "single_image_camera_pan_" + name,
				mode:       "single_image",
				motionID:   direction.ID,
				axis:       direction.Axis,
				durationMS: 5000,
				item:       singleItem(*assetsRoot, direction.ID, 5000),
			},
		)
	}

	manifest := map[string]any{
		"canvas":        []int{1920, 1080},
		"fps":           30,
		"motion_family": "image_group_camera_pan",
		"premium": map[string]any{
			"group_push_in":   "1.02 -> 1.06",
			"travel_fraction": "60%",
			"hold_fraction":   "40%",
			"single_scale":    "1.06 -> 1.0",
		},
		"clips": []any{},
	}
	background := assetRef(*assetsRoot, "premium_background", outputSlug+"/assets/premium_background.png")
	for _, c := range clips {
		raw, err := json.Marshal(map[string]any{
			"schema_version": "renderinggen.overlay-plan.v1",
			"plan_id":        c.name,
			"video_id":       c.name,
			"width":          1920,
			"height":         1080,
			"fps_num":        30,
			"fps_den":        1,
			"duration_ms":    c.durationMS,
			"background": map[string]any{
				"kind":       "image",
				"asset_refs": []any{background},
				"fit":        "cover",
			},
			"items": []any{c.item},
		})
		if err != nil {
			fail(err)
		}
		compiled, err := overlay.CompileSemantic(raw)
		if err != nil {
			fail(fmt.Errorf("%s: %w", c.name, err))
		}
		plan, err := json.MarshalIndent(compiled.Plan, "", "  ")
		if err != nil {
			fail(err)
		}
		path := filepath.Join(*out, c.name+".plan.json")
		if err := os.WriteFile(path, append(plan, '\n'), 0o644); err != nil {
			fail(err)
		}
		manifest["clips"] = append(manifest["clips"].([]any), map[string]any{
			"id": c.name, "mode": c.mode, "axis": c.axis,
			"motion_id": c.motionID, "plan": filepath.Base(path),
			"duration_ms": c.durationMS, "frames": c.durationMS * 30 / 1000,
		})
		fmt.Println(path)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "manifest.json"), append(encoded, '\n'), 0o644); err != nil {
		fail(err)
	}
}

func singleItem(assetsRoot, motionID string, durationMS int) map[string]any {
	return map[string]any{
		"id": "image", "kind": "image", "template_id": "image_popup",
		"preset_id": "image_focus_in", "composition_id": "single_image",
		"motion_id": motionID, "start_ms": 0, "end_ms": durationMS,
		"params":     map[string]any{"width": 720, "height": 820, "fit": "contain", "position_x": 0, "position_y": 0},
		"asset_refs": []any{assetRef(assetsRoot, "portrait_1", outputSlug+"/assets/portrait_1.png")},
	}
}

func stripItem(assetsRoot, motionID string, durationMS int) map[string]any {
	var refs, layers []any
	for index := 1; index <= 3; index++ {
		id := fmt.Sprintf("portrait_%d", index)
		url := fmt.Sprintf("%s/assets/%s.png", outputSlug, id)
		refs = append(refs, assetRef(assetsRoot, id, url))
		layers = append(layers, map[string]any{
			"id": id, "asset_id": id, "start_ms": 0, "end_ms": durationMS,
			"preset_id": "image_focus_in", "motion_id": "",
			"params": map[string]any{"width": 720, "height": 820, "fit": "contain", "position_x": 0, "position_y": 0},
		})
	}
	return map[string]any{
		"id": "strip", "kind": "entity_image", "template_id": "image_popup",
		"preset_id": "image_focus_in", "composition_id": "multiple_images",
		"group_motion_id": motionID, "start_ms": 0, "end_ms": durationMS,
		"asset_refs": refs, "image_layers": layers,
	}
}

func assetRef(assetsRoot, id, url string) map[string]any {
	data, err := os.ReadFile(filepath.Join(assetsRoot, filepath.FromSlash(url)))
	if err != nil {
		fail(fmt.Errorf("asset %s: %w", url, err))
	}
	sum := sha256.Sum256(data)
	return map[string]any{
		"asset_id": id, "sha256": hex.EncodeToString(sum[:]),
		"url": url, "media_type": "image/png",
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "camera-pan-demo:", err)
	os.Exit(1)
}
