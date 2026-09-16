# Mike Tyson overlay presets — runtime render

This folder contains a render-only pass over the already fixed English
2,000-word Mike Tyson request. It does not regenerate the narration or audio.

`manifest.json` is the batch manifest of record
(`renderinggen.batch-manifest.v1`). `cmd/batch-build -request` builds it from the
request's own extraction block: it takes the ten phrase candidates, checks that
each one still appears in its matching source segment, and emits ten text
overlays plus five entity image overlays whose asset hashes it recomputes. Those
phrases are fixed for this preset preview; the final production flow still lets
NLP select phrases from the fixed script.

The run renders 10 text overlays with `apple_v2` and distinct official
`motion_id` values, plus 5 entity image overlays with the official image
presets. Each image preset enters over **37 frames = 1.5 s at 24 fps** (focus and
scale reveal a larger scale change, slide presets travel 360 px, all four
combine with a fade), and the renderer evaluates the motion at runtime from the
plan's `preset_id`. Each clip is 5 seconds at 1920×1080, 24 fps, on the existing
pale olive background (`#EEF1E7`); the text preset supplies white type with a
dark stroke and shadow.

## Deliverables

- `manifest.json` — the batch master of record (plans + content-addressed assets
  + the reporting metadata the run report is built from).
- `plans/` — the semantic `overlay-plan.v1` of every job, as built.
- `renders/phrases/`, `renders/images/` — the rendered clips, named
  `<batch>-<job>.mp4`. The raw per-frame timing sidecar sits beside each render
  (`<batch>-<job>.mp4.timing.json`); it is diagnostics and the repository keeps it
  out of git.
- `docs/timing.json` — the run report: queue timings, preset/motion ids, entrance
  window, artifact hashes, and the location of each render.
- `docs/jobs_snapshot.json` — the final queue body of every job.
- `docs/verify_report.json` — `cmd/batch-verify`'s certification: structural
  facts, content addresses, and the glyph-ink check that proves the text
  rasterised.

## Image provenance

- Mike Tyson and Cus D’Amato: new editorial illustrations generated for this
  project; they are illustrations, not historical photographs.
- [Muhammad Ali portrait, 1962](https://commons.wikimedia.org/wiki/File:Muhammad_Ali_Smiling_1962_Portrait.jpg)
- [Sugar Ray Robinson, 1965](https://commons.wikimedia.org/wiki/File:Sugar_Ray_Robinson_1965_(cropped).jpg)
- [Joe Frazier press portrait, 1971](https://commons.wikimedia.org/wiki/File:Joe_Frazier_1971_Press_Photo.jpg)

The Commons source pages carry the image descriptions and reuse-status notes.
Attribution and source URLs are also recorded in `manifest.json` and
`docs/timing.json`.

## Run

The whole flow is Go: `cmd/batch-build` (manifest), `cmd/batch-run` (submit,
wait, download, report), `cmd/batch-verify` (certify). `batch-run` can serve the
corpus assets itself, so no separate file server is needed.

From the RenderingGen repository root, with the queue (:8081) and the worker
running:

```bash
cd renderinggen
go build -o bin/batch-build ./cmd/batch-build
go build -o bin/batch-run   ./cmd/batch-run
go build -o bin/batch-verify ./cmd/batch-verify

cd ../mike_tyson_overlay_test/preset_overlays_v1

# 1. manifest of record (15 jobs). -repo-root is the RenderingGen checkout: the
#    builder hashes the entity images and the two fonts from it.
../../renderinggen/bin/batch-build \
  -request ../mike_tyson_2000w_5entities_10phrases_en_sourcebacked_e2b_request.json \
  -batch-id tyson-overlays-$(date -u +%Y%m%d)-v1 \
  -asset-base-url http://127.0.0.1:8099 -repo-root ../.. \
  -out manifest.json -plans-dir plans

# 2. render it: submit, wait, download, report. -serve-assets serves the checkout
#    over HTTP for the duration of the run (the worker self-heals a missing
#    asset from the manifest's source_url and verifies its SHA-256).
../../renderinggen/bin/batch-run -manifest manifest.json \
  -report docs/timing.json -snapshot docs/jobs_snapshot.json \
  -download-dir renders -serve-assets ../.. -serve-addr 127.0.0.1:8099

# 3. certify the artifacts: structure + content address + glyph ink.
../../renderinggen/bin/batch-verify -manifest manifest.json \
  -report docs/verify_report.json -stage /tmp/tyson-verify
```

Exit codes are meaningful: `batch-run` fails if any job is not terminal, and
`batch-verify` fails on any structural mismatch, hash mismatch, empty phrase band
or language collision.
