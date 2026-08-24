package chronon

// ExampleColorSmokePlan is a minimal, asset-free chronon.render-plan.v1
// document: a single solid-color frame, rendered at 320x180@1fps. Because it
// references no external assets it exercises the whole render path (plan ->
// compile -> software rasterize -> h264 encode) with no asset root required,
// which makes it the canonical smoke plan for the CLI and end-to-end
// integration tests against the real chronon3d_cli binary.
const ExampleColorSmokePlan = `{
  "schema": "chronon.render-plan",
  "version": 1,
  "job_id": "color-smoke",
  "canvas": { "width": 320, "height": 180, "fps_num": 1, "fps_den": 1, "duration_frames": 1 },
  "layers": [
    { "id": "background", "type": "color", "color": [0.08, 0.12, 0.25, 1.0] }
  ],
  "output": { "path": "result.mp4", "format": "mp4", "codec": "h264" }
}`
