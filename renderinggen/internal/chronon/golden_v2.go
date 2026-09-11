package chronon

// GoldenOverlayJobV2 is the canonical, immutable universal benchmark job for
// the REAL RenderingGen workload — the successor of the retired v1 golden that
// exercises the full semantic vocabulary and every canonical primitive:
//
//	video background (background.mp4, full 8s)      → Video primitive
//	+ IMPORTANT_PHRASE ×2 (caption_card, animated)
//	+ IMPORTANT_WORD ×2   (active_word_pop, animated)
//	+ IMAGE_OVERLAY ×2    (contain, popup)          → Image primitive
//	+ LOGO                (contain, corner)
//	+ 4 layer animations  (fade_in / scale_in / slide_up / fade_in)
//
// 8 seconds at 30 fps = 240 frames (f0-239) on a 1280x720 canvas. The assets
// are deterministic fixtures under testdata/golden/ (see
// infra/e2e/gen-golden-v2-assets.py) with content-addressed hashes baked in;
// the text layers carry no font: Chronon's VisualPresetRegistry resolves the
// preset's canonical font asset, and the bytes travel as a queue asset:
//
//	background.mp4              11eeec4e47fb254dc6acc4b72405bf06f2ed7294cffa8b7a95ba76f1e8d9c70c
//	overlay_globe.png           b7219a0c2f3f0c45f12d3b387332bd2cf0502205a6bb3fa0beca542de6da1939
//	overlay_chart.png           efb77ea94d50f178e970841f641be7f4efc59837d5628fec2f4bdb6e88a9f73a
//	logo_pulse.png              15d17403acaf45cdeeb9dad6a6c88e3b5f686b3ee1961cc97815b4830d3b981f
//	assets/fonts/DejaVuSans.ttf 690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648
//
// This document must stay byte-identical to
// RenderingGen/testdata/golden/golden-overlay-job-v2.json: it is the golden
// reference used to compare CLI, daemon, CPU, GPU (Vulkan), cold/warm cache
// and visual regressions across every future Chromon / PipelineGen version.
// Do not edit it casually — regenerate deliberately and update BOTH copies.
const GoldenOverlayJobV2 = `{
  "id": "golden-overlay-v2",
  "schema": "renderinggen.job",
  "version": 1,
  "render_plan": {
    "schema": "chronon.render-plan.v2",
    "version": 2,
    "job_id": "golden-overlay-v2",
    "canvas": { "width": 1280, "height": 720, "fps_num": 30, "fps_den": 1, "duration_frames": 240 },
    "layers": [
      {
        "id": "background_video",
        "type": "video",
        "source": "assets/background.mp4",
        "size": [1280, 720],
        "fit": "cover",
        "start_frame": 0,
        "duration_frames": 240
      },
      {
        "id": "important_phrase_1",
        "type": "text",
        "text": "IL FUTURO È ADESSO",
        "style": { "font": "assets/fonts/DejaVuSans.ttf", "font_size": 54, "fill": "#FFFFFF" },
        "start_frame": 24,
        "duration_frames": 84,
        "animation": { "tracks": [{ "property": "opacity", "keyframes": [{ "frame": 24, "value": 0 }, { "frame": 48, "value": 1 }] }] }
      },
      {
        "id": "important_word_1",
        "type": "text",
        "text": "VELOCITÀ",
        "style": { "font": "assets/fonts/DejaVuSans.ttf", "font_size": 64, "fill": "#FFFFFF" },
        "start_frame": 24,
        "duration_frames": 84,
        "animation": { "tracks": [{ "property": "scale", "keyframes": [{ "frame": 24, "value": 0.8 }, { "frame": 48, "value": 1 }] }] }
      },
      {
        "id": "image_overlay_1",
        "asset": "assets/overlay_globe.png",
        "type": "image",
        "size": [300, 300],
        "position": [380, 0],
        "start_frame": 24,
        "duration_frames": 132
      },
      {
        "id": "important_phrase_2",
        "type": "text",
        "text": "CAMBIARE IL MERCATO",
        "style": { "font": "assets/fonts/DejaVuSans.ttf", "font_size": 54, "fill": "#FFFFFF" },
        "start_frame": 132,
        "duration_frames": 84,
        "animation": { "tracks": [{ "property": "position_y", "keyframes": [{ "frame": 132, "value": 40 }, { "frame": 156, "value": 0 }] }] }
      },
      {
        "id": "important_word_2",
        "type": "text",
        "text": "POTENZA",
        "style": { "font": "assets/fonts/DejaVuSans.ttf", "font_size": 64, "fill": "#FFFFFF" },
        "start_frame": 132,
        "duration_frames": 84,
        "animation": { "tracks": [{ "property": "opacity", "keyframes": [{ "frame": 132, "value": 0 }, { "frame": 156, "value": 1 }] }] }
      },
      {
        "id": "image_overlay_2",
        "asset": "assets/overlay_chart.png",
        "type": "image",
        "size": [300, 300],
        "position": [840, 380],
        "start_frame": 120,
        "duration_frames": 108
      },
      {
        "id": "logo",
        "type": "image",
        "asset": "assets/logo_pulse.png",
        "size": [160, 160],
        "fit": "contain",
        "position": [1060, 40],
        "start_frame": 0,
        "duration_frames": 240
      }
    ],
    "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
  },
  "assets": [
    {
      "hash": "11eeec4e47fb254dc6acc4b72405bf06f2ed7294cffa8b7a95ba76f1e8d9c70c",
      "logical_path": "assets/background.mp4"
    },
    {
      "hash": "b7219a0c2f3f0c45f12d3b387332bd2cf0502205a6bb3fa0beca542de6da1939",
      "logical_path": "assets/overlay_globe.png"
    },
    {
      "hash": "efb77ea94d50f178e970841f641be7f4efc59837d5628fec2f4bdb6e88a9f73a",
      "logical_path": "assets/overlay_chart.png"
    },
    {
      "hash": "15d17403acaf45cdeeb9dad6a6c88e3b5f686b3ee1961cc97815b4830d3b981f",
      "logical_path": "assets/logo_pulse.png"
    },
    {
      "hash": "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648",
      "logical_path": "assets/fonts/DejaVuSans.ttf"
    }
  ]
}`
