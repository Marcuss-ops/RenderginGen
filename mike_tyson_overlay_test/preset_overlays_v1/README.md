# Mike Tyson overlay presets — runtime render

This folder contains a render-only pass over the already fixed English
2,000-word Mike Tyson request. It does not regenerate the narration or audio.

`scripts/render_overlays.py` reads the ten phrase candidates carried in the
request's extraction block and checks that each phrase remains in its matching
source segment. Those phrases are used for this preset preview; the final
production flow still lets NLP select phrases from the fixed script.

The run renders 10 text overlays with `apple_v2` and distinct official
`motion_id` values, plus 5 entity image overlays with the official image
presets. The renderer evaluates these motions at runtime. Each clip is 5
seconds at 1920×1080, 24 fps, on the existing pale olive background
(`#EEF1E7`). The text preset supplies white type with a dark stroke and shadow.

## Deliverables

- `renders/phrases/` — ten animated phrase clips.
- `renders/images/` — five animated entity portrait clips.
- `docs/timing.json` — queue timings, preset ids, motion ids, artifact hashes,
  and the location of each raw timing sidecar.
- `docs/timing/` — the renderer's per-frame timing JSON for each clip.
- `docs/render_manifest.json` and `docs/jobs_snapshot.json` — generated plans
  and final queue records.

## Image provenance

- Mike Tyson and Cus D’Amato: new editorial illustrations generated for this
  project; they are illustrations, not historical photographs.
- [Muhammad Ali portrait, 1962](https://commons.wikimedia.org/wiki/File:Muhammad_Ali_Smiling_1962_Portrait.jpg)
- [Sugar Ray Robinson, 1965](https://commons.wikimedia.org/wiki/File:Sugar_Ray_Robinson_1965_(cropped).jpg)
- [Joe Frazier press portrait, 1971](https://commons.wikimedia.org/wiki/File:Joe_Frazier_1971_Press_Photo.jpg)

The Commons source pages carry the image descriptions and reuse-status notes.
Attribution and source URLs are also recorded in `docs/render_manifest.json`
and `docs/timing.json`.

## Run

From the `RenderingGen` repository root, serve the repository files at the
asset URL used by the worker, then run:

```bash
python3 -m http.server 8099 --bind 0.0.0.0
python3 mike_tyson_overlay_test/preset_overlays_v1/scripts/render_overlays.py
```

The script submits real overlay-render jobs to the live queue, downloads each
completed MP4 and the raw timing sidecar, and records the results under
`docs/` and `renders/`.
