package chronon

// GoldenSemanticOverlayJobV1 is the canonical, immutable SEMANTIC golden job:
// the same workload as GoldenOverlayJobV1 (background + phrase + word + image
// overlay) but expressed in PipelineGen's renderinggen.overlay-plan.v1
// contract. The worker's CompileIfSemantic lowers it into a concrete
// chronon.render-plan.v1 and runs the exact same materialize -> plan.json ->
// chronon3d_cli -> artifact chain as the concrete golden.
//
//	background.jpg (IMAGE_OVERLAY, full 5s, cover)
//	+ "QUESTO CAMBIA TUTTO" (IMPORTANT_PHRASE, caption_card, f20-60)
//	+ "APPLE"               (IMPORTANT_WORD,   active_word_pop, f65-95)
//	+ apple.png             (IMAGE_OVERLAY, image_focus_in, f90-135)
//
// 5 seconds at 30 fps = 150 frames on a 1280x720 canvas. The assets are the
// same deterministic fixtures as GoldenOverlayJobV1 under testdata/golden/
// with content-addressed sha256 baked into asset_refs; the job envelope also
// carries them by hash so the object store can seed them (real submissions
// resolve the refs the same way).
//
//	background.jpg            52209ee36928dba960583179922a54acf045d52d44c3128c517425d4baaa4f78
//	apple.png                 ed873745e76173b66999c63546770d9f1426a2189515149176c67637e99a62d6
//	assets/fonts/DejaVuSans.ttf  690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648
//
// This document must stay byte-identical to
// RenderingGen/testdata/golden/golden-semantic-overlay-job-v1.json. It is the
// golden reference for the semantic path: if the compiler, the preset
// vocabulary or the asset pipeline changes, this job must keep compiling and
// rendering. Do not edit it casually — regenerate deliberately and update
// BOTH copies.
const GoldenSemanticOverlayJobV1 = `{
  "id": "golden-semantic-overlay-v1",
  "schema": "renderinggen.job",
  "version": 1,
  "job_type": "overlay.render",
  "render_plan": {
    "schema_version": "renderinggen.overlay-plan.v1",
    "plan_id": "golden-semantic-overlay-v1",
    "video_id": "golden-semantic-overlay-v1",
    "width": 1280,
    "height": 720,
    "fps_num": 30,
    "fps_den": 1,
    "items": [
      {
        "id": "background",
        "template_id": "IMAGE_OVERLAY",
        "preset_id": "image_focus_in",
        "start_ms": 0,
        "end_ms": 5000,
        "params": {
          "width": 1280,
          "height": 720,
          "fit": "cover"
        },
        "asset_refs": [
          {
            "asset_id": "background",
            "sha256": "52209ee36928dba960583179922a54acf045d52d44c3128c517425d4baaa4f78",
            "url": "https://store.example/objects/background.jpg",
            "media_type": "image/jpeg"
          }
        ]
      },
      {
        "id": "important_phrase",
        "template_id": "IMPORTANT_PHRASE",
        "preset_id": "caption_card",
        "text": "QUESTO CAMBIA TUTTO",
        "start_ms": 667,
        "end_ms": 2000
      },
      {
        "id": "important_word",
        "template_id": "IMPORTANT_WORD",
        "preset_id": "active_word_pop",
        "text": "APPLE",
        "start_ms": 2167,
        "end_ms": 3167
      },
      {
        "id": "image_overlay",
        "template_id": "IMAGE_OVERLAY",
        "preset_id": "image_focus_in",
        "start_ms": 3000,
        "end_ms": 4500,
        "params": {
          "width": 260,
          "height": 260,
          "fit": "contain"
        },
        "asset_refs": [
          {
            "asset_id": "apple",
            "sha256": "ed873745e76173b66999c63546770d9f1426a2189515149176c67637e99a62d6",
            "url": "https://store.example/objects/apple.png",
            "media_type": "image/png"
          }
        ]
      }
    ]
  },
  "assets": [
    {
      "hash": "52209ee36928dba960583179922a54acf045d52d44c3128c517425d4baaa4f78",
      "logical_path": "assets/background.jpg"
    },
    {
      "hash": "ed873745e76173b66999c63546770d9f1426a2189515149176c67637e99a62d6",
      "logical_path": "assets/apple.png"
    },
    {
      "hash": "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648",
      "logical_path": "assets/fonts/DejaVuSans.ttf"
    }
  ]
}`
