# Multilingual overlays v1 — 10 translated overlays × 10 languages

Overlay-only certification corpus: **10 overlays (5 phrases + 5 images) rendered
once per language for the 10 languages configured in PipelineGen**
(`media.multilingual.languages`: `it, en, pl, ru, de, es, pt-BR, fr, tr, id`),
on the production path — central queue → worker → warm Chronon3d daemon
(Vulkan + NVENC) — then published to Google Drive, one subfolder per language.

- Batch of record: `ml-overlays-10x10-run2` (100 jobs, `manifest.json`)
- Texts of record: `translations.json` (Argos, en→target)
- Evidence: `evidence/run2_certified_summary.json`,
  `evidence/run2_verify_report.json`, `evidence/drive_publication.json`
- Counter-run kept on purpose: `evidence/run1_without_fallback_font_summary.json`

## Matrix

| Family | Overlays | Preset / motion |
|---|---|---|
| Phrase (text) | `phrase_01_lower_third_safe` … `phrase_05_slide_left_punch` | official text preset `apple_v2` + 5 distinct official motions (`liquid_glass_ripple`, `kinetic_stamp_impact`, `editorial_push_in`, `neon_flicker_ignite`, `hologram_scanline_build`) |
| Image | `image_focus_in`, `image_fade_in`, `image_scale_in`, `image_slide_left`, `image_slide_right` | the 5 official image presets, over `testdata/golden/gerard_butler.jpg` |

The 5 phrase **texts** are the ones carried by the checked-in
`phrase_preset_videos/` corpus. Their legacy preset names (`lower_third_safe`,
`clean_slide_up`, `scale_pop`, `dm_sans_fade`, `slide_left_punch`) are **not** in
RenderingGen's official catalog, and the queue accepts only certified presets
(`overlay.resolveOfficialPreset`), so the corpus texts are rendered through
`apple_v2` — each with a distinct official motion so the five overlays stay
visually distinct. Image overlays are language-independent by construction:
their bytes are identical in all ten languages (5 distinct hashes), while every
phrase overlay whose translated text differs does produce different bytes
(verified: 0 collisions over different texts).

## Reproduce

Prerequisites: `renderinggen-queue` (:8081), `renderinggen` worker,
`chronon3d` daemon (Vulkan/NVENC), and an HTTP(S) base URL that serves the
font/image assets (the worker self-heals a missing asset from `source_url` and
verifies its SHA-256 while streaming).

The whole flow is Go: `cmd/batch-build` (manifest), `cmd/batch-run` (submit,
wait, download, report), `cmd/batch-verify` (certify), `cmd/drive-upload`
(publish). The phrases' translations are data (`translations.json`, produced by
PipelineGen's Argos + Ollama chain); the commands below never invent text.

```bash
cd renderinggen
go build -o bin/batch-build ./cmd/batch-build
go build -o bin/batch-run ./cmd/batch-run
go build -o bin/batch-verify ./cmd/batch-verify
go build -o bin/drive-upload ./cmd/drive-upload

# 1. build the manifest (100 jobs: 5 phrases × 10 languages + 5 images × 10).
#    -repo-root is the RenderingGen checkout (font/image hashing).
./bin/batch-build -translations ../multilingual_overlays_v1/translations.json \
  -batch-id ml-overlays-10x10-run2 -asset-base-url http://127.0.0.1:8099 \
  -repo-root .. -out ../multilingual_overlays_v1/manifest.json \
  -plans-dir ../multilingual_overlays_v1/plans

# 2. render it on the production path, wait, download and measure.
#    -serve-assets serves testdata/golden (where the corpus assets live).
cd ../multilingual_overlays_v1
../renderinggen/bin/batch-run -manifest manifest.json \
  -report summary.json -snapshot jobs_snapshot.json \
  -download-dir artifacts -serve-assets ../testdata/golden \
  -serve-addr 127.0.0.1:8099

# 3. verify structure, content addresses and the translated-text pixel band
#    (exit 1 on any FAIL). Add -baseline-manifest to compare against the run
#    without the coverage font.
../renderinggen/bin/batch-verify -manifest manifest.json \
  -report verify_report.json -stage verify

# 4. publish to Drive (one subfolder per language). One call per file; the
#    provider must account for every byte (-sha256) or the upload is refused.
../renderinggen/bin/drive-upload -credentials <oauth.json> -token <token.json> \
  -folder 1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS -subfolder <lang> \
  -file artifacts/<job>/<batch>-<job>.mp4 -name <batch>-<job>.mp4 \
  -sha256 <artifact_hash from summary.json>
```

## The requirement this corpus encodes: a job must declare its coverage font

Chronon3d resolves the fallback font stack by scanning **the primary font's own
directory** — `bundled_font_root_for(font)` in
`src/scene/builders/text_run_builder.cpp` returns
`<path-of-the-primary-font>/../`. The worker resolves plan assets relative to
the **job workspace** (`renderinggen/internal/processor/gpu_run.go`:
`AssetsRoot = prepared.Workspace.Root()`), so any font a job does not declare
does not exist for the shaper.

Consequence, measured: a translated overlay whose text leaves the primary
font's coverage fails inside the daemon —
`[font-fallback] Missing glyph … no font in stack covers all visible
codepoints` → `draw_packed_text_run_surface: empty glyph vector` — and, when the
run still produced an artifact, it rasterised almost nothing:

| Overlay (ru) | run1 (primary font only) | run2 (primary + `Inter-Bold`) |
|---|---|---|
| `phrase_01_lower_third_safe` | completed, **ink 790 px** | completed, **ink 28 785 px** |
| `phrase_04_dm_sans_fade` | completed, **ink 1 297 px** | completed, **ink 38 045 px** |
| `phrase_02_clean_slide_up` | **failed** (exit 1 at frame 0) | completed, **ink 35 880 px** |

Therefore **every phrase job declares both fonts**:
`assets/fonts/Poppins-Bold.ttf` (the preset's primary) and
`assets/fonts/Inter-Bold.ttf` (Chronon3d's own bundle font, which covers
Cyrillic and Latin-extended: tr `ı/ş/ğ`, pt `ã`). This is a job-data
requirement, not an engine change. `cmd/batch-verify` fails any artifact whose
phrase band carries fewer than `-min-ink` glyph pixels (default 5000), which is
exactly the silent truncation the counter-run produced.

## Measured result (run2, RTX A4000, worker `gpu_lanes: 2`)

| Metric | Value |
|---|---|
| Jobs | **100/100 completed**, 0 failed, 0 retries beyond attempt 1 |
| Wall (first queued → last completed) | **230.3 s** |
| Throughput | **26.06 overlays/min** = **52.1 fps** of finished overlay video (12 000 frames) |
| Backend | `vulkan` for 100/100; 120/120 frames GPU-native (`vulkan` + `nvenc`), 0 software-fallback nodes |
| Chronon job wall (in-daemon) | phrase p50 **1 109 ms**, image p50 **1 560 ms** |
| Chronon realtime factor | phrase p50 **4.51×**, image p50 **3.21×** (mean frame 2.65 ms, 0 frames over the 41.67 ms budget) |
| Per language | 10/10 completed for each of `it, en, pl, ru, de, es, pt-BR, fr, tr, id` |
| Claim → completion p50 | 11.38 s (2 GPU lanes, 3 pipeline workers: jobs are claimed faster than the lane drains) |
| Distinct artifact hashes | 49 (44 phrase + 5 image) — the 6 collisions are languages whose Argos output is byte-identical |

The counter-run (run1, before the fallback font was declared) is the same matrix
minus the coverage font: 97/100 in 712.8 s, i.e. **3.1× slower end-to-end** —
the retries added latency, and two of the three Russian overlays shipped with
their text effectively missing.

## Acceptance criteria

| Criterion | Result |
|---|---|
| 10 overlays × 10 languages all rendered on the production path | **PASS** (100/100, `vulkan`) |
| Translated text actually rasterised in the artifact | **PASS** (ink pixel check, run1 vs run2) |
| Same overlay, different text ⇒ different bytes | **PASS** (0 collisions) |
| Output contract 1920×1080 / 24 fps / 120 frames / 5 s / content-address verified | **PASS** (100/100) |
| Maximum-speed evidence, no estimates | **PASS** (queue timestamps + certified artifact metrics) |
| Published to the target Drive folder, one subfolder per language | **PASS** (100/100, `evidence/drive_publication.json`) |

## Known gaps (named, not hidden)

- **Translation quality is Argos-level**, and Argos degrades on input that is not
  sentence case: all-caps sources are translated title-cased and re-uppercased
  (`cmd/batch-build`/translation step), which fixed `BREAKING NEWS` in `es`/`pt-BR`/`ru`
  but still leaves `it`/`pl`/`de`/`id` **untranslated** (identical to the source)
  and `tr` wrong (`YOK NEWS`). `phrase_05` is also weak in `tr`
  (`Altyapı Altyapı`) and `phrase_02` is partially untranslated in `id`. The
  documented escape hatch already exists in PipelineGen's translation chain
  (Argos primary + Ollama quality fallback, `internal/capabilities/translation`):
  a quality gate on the Argos output is the follow-up, not a second engine
  invented here.
- **Script coverage is certified only for Latin + Cyrillic.** Arabic, Hebrew and
  CJK are in Chronon3d's font bundle but no language in this matrix exercises
  them; a corpus addition is needed before claiming those scripts.
- **Assets came from a local HTTP source** in this run (font + image served on
  `127.0.0.1:8099` for the worker's self-heal path). A production run should
  stage the same bytes in the artifact store and reference them by hash without
  a `source_url`.
- **Drive publication is operator-driven.** The worker's own Drive publishing is
  disabled in `/etc/renderinggen/renderinggen.yaml` (`drive.enabled: false`), so
  the corpus was published with `cmd/drive-upload` and the operator's OAuth
  token; enabling worker-side publication is a deployment decision.
- **Not a Strategy-A batch.** This corpus renders each overlay independently
  (100 overlay jobs) rather than one shared base + per-language overlay passes
  (`renderinggen.batch-multilingual.v1`). The two shapes answer different
  questions: this one measures the translated-overlay lane itself.
