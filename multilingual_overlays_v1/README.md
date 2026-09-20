# Multilingual overlays v1 — 10 translated overlays × 10 languages

Overlay-only certification corpus: **10 overlays (5 phrases + 5 images) rendered
once per language for the 10 languages configured in PipelineGen**
(`media.multilingual.languages`: `it, en, pl, ru, de, es, pt-BR, fr, tr, id`)
on the central queue → worker → warm Chronon3d daemon (Vulkan + NVENC). The
historical Argos batch was published to Drive; the fresh Ollama batch is rendered
and verified locally, and awaits its own Drive publication.

- Previous certified batch: `ml-overlays-10x10-run2` (100 jobs, historical Argos
  translations; see the run2 evidence below)
- Current phrase source: `translations.json` (each target translated directly
  from the English `source` with local Ollama; `translation_provenance.json`
  records hashes, model, provider, and call times)
- Current render batch: `ml-overlays-ollama-20260917` (**100/100 completed**, all
  Vulkan, text-pixel verifier PASS; see the current reports below)
- Current evidence: `manifest_ollama_20260917.json`,
  `summary_ollama_20260917.json`, `verify_report_ollama_20260917.json`, and
  `translation_provenance.json`; local videos and timing sidecars are in
  `artifacts_ollama_20260917/`
- VRAM calibration matrix (an INPUT, not evidence):
  `manifests/vram_calibration_suite_2026-09-17.json` — the certified shapes ×
  ≥3 repetitions measured with `vram-probe -mode calibration-suite`; the suite
  writes its report into `evidence/`, next to the other `vram_*` documents
- Evidence: `evidence/run2_certified_summary.json`,
  `evidence/run2_verify_report.json`, `evidence/drive_publication.json`
- Serial video-source determinism control: two sequential renders on one daemon,
  `evidence/video_source_serial_determinism_2026-09-17.json`; the separate
  two-runtime VRAM shape remains incomplete because concurrent outputs differ.
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

The render flow is Go: `cmd/batch-build` (manifest), `cmd/batch-run` (submit,
wait, download, report), `cmd/batch-verify` (certify), and `cmd/drive-upload`
(publish). `scripts/localize_with_ollama.py` translates every target directly
from each English `source` with local Ollama, ignores previous translations,
and records hashes/model/provider/timings in `translation_provenance.json`. It
does not call Argos. PipelineGen's translated-script path then runs NLP over
the translated text to extract localized entities.

```bash
cd renderinggen
go build -o bin/batch-build ./cmd/batch-build
go build -o bin/batch-run ./cmd/batch-run
go build -o bin/batch-verify ./cmd/batch-verify
go build -o bin/drive-upload ./cmd/drive-upload

# 0. Regenerate the five phrases directly from English using local Ollama.
cd ..
python3 multilingual_overlays_v1/scripts/localize_with_ollama.py \
  --input multilingual_overlays_v1/translations.json \
  --output multilingual_overlays_v1/translations.json \
  --provenance multilingual_overlays_v1/translation_provenance.json
cd renderinggen

# 1. build the manifest (100 jobs: 5 phrases × 10 languages + 5 images × 10).
#    -repo-root is the RenderingGen checkout (font/image hashing).
./bin/batch-build -translations ../multilingual_overlays_v1/translations.json \
  -batch-id ml-overlays-ollama-20260917 -asset-base-url http://127.0.0.1:8099 \
  -repo-root .. -out ../multilingual_overlays_v1/manifest_ollama_20260917.json \
  -plans-dir ../multilingual_overlays_v1/plans_ollama_20260917

# 2. render it on the production path, wait, download and measure.
#    -serve-assets serves testdata/golden (where the corpus assets live).
cd ../multilingual_overlays_v1
../renderinggen/bin/batch-run -manifest manifest_ollama_20260917.json \
  -report summary_ollama_20260917.json \
  -snapshot jobs_snapshot_ollama_20260917.json \
  -download-dir artifacts_ollama_20260917 -serve-assets ../testdata/golden \
  -serve-addr 127.0.0.1:8099

# 3. verify structure, content addresses and the translated-text pixel band
#    (exit 1 on any FAIL). Add -baseline-manifest to compare against the run
#    without the coverage font.
../renderinggen/bin/batch-verify -manifest manifest_ollama_20260917.json \
  -report verify_report_ollama_20260917.json -stage verify

# 4. publish to Drive (one subfolder per language). One call per file; the
#    provider must account for every byte (-sha256) or the upload is refused.
../renderinggen/bin/drive-upload -credentials <oauth.json> -token <token.json> \
  -folder 1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS -subfolder <lang> \
  -file artifacts_ollama_20260917/<family>/<batch>-<job>.mp4 \
  -name <batch>-<job>.mp4 \
  -sha256 <artifact_hash from summary_ollama_20260917.json>
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

### The same rule, enforced by the builder (2026-09-20)

A job must declare **the fonts its own plan burns**, because the plan is what
decides which font the shaper asks for. `cmd/batch-build` now proves that instead
of assuming it: it lowers every job's plan with
`renderbatch.CompileRenderPlan` and refuses the whole manifest when a plan names a
font the job does not declare (`overlaybatch.checkPlanFonts`), and the extra font
is derived from the same owner the plan compiler consults
(`overlay.OfficialFontPathForLanguage`), so the manifest and the plan cannot
drift apart again.

Cyrillic jobs therefore declare **three** fonts — the Latin pair above plus
`assets/fonts/DejaVuSans.ttf`, the compiler's primary wherever
`overlay.OfficialFontPathForLanguage` selects Cyrillic (`ru`, `uk`, `bg`, `sr`,
`mk`, `be`, `kk`, `ky`, `tg`, `mn`, `az`, `uz`) — while Latin jobs keep the pair.

Measured regression this closes: the `production_rehearsal_v1` runtime2 batch ran
ten phrase jobs (one per language) and reported 9 completed / 1 failed — the
`phrase_01_lower_third_safe__ru` row carried the daemon's
`render job failed with exit code 1` — because its ru jobs still declared the
Latin pair after the compiler had moved Cyrillic to DejaVuSans. With the
declaration derived as above, the same 613-character ru text renders 1/1
completed on the first attempt: 77 311 ink px, `cmd/batch-verify` PASS.

## Current measured result (Ollama, 2026-09-17)

| Metric | Value |
|---|---|
| Jobs | **100/100 completed**, 0 failed, one attempt each |
| Queue wall | **155.2 s**; 38.67 overlays/min = 77.3 output frames/s (12,000 frames) |
| Renderer | `vulkan` and `full_graph_native` for 100/100; 0 execution downgrades |
| GPU timing | 12,000/12,000 frames attributed; median device span 0.358 ms (phrase) / 0.286 ms (image); zero queue wait and encoder backpressure |
| Chronon realtime factor | phrase p50 **5.79×**, image p50 **3.60×** |
| Frame timing | phrase p50 p95-frame **1.87 ms**, image p50 p95-frame **1.87 ms** (41.67 ms budget) |
| Text rasterization | verifier ink range **12,364–52,600 px**, all above the 5,000 px floor |
| Distinct artifact hashes | **54**: 49 phrase + 5 language-independent images; the Spanish/English Nolan lower-third is the same valid text |
| Languages | 10/10 jobs for each of `it, en, pl, ru, de, es, pt-BR, fr, tr, id` |
| Output contract | H.264, 1920×1080, 24 fps, 120 frames, 5 s; 100/100 structural/content-address checks PASS |

The 100 timing sidecars and both JSON reports are retained beside this README.
`summary_ollama_20260917.json` contains per-job queue, render, backend and
artifact data; `verify_report_ollama_20260917.json` includes per-artifact text
ink and distinctness results. The old batch is kept below as a comparison
baseline and its Drive publication record is not reused for this batch.

## Historical measured result (run2 used the former Argos translations)

These run2 metrics certify the rendering path and GPU lane. Their phrase pixels
used the former Argos translations, so they are a baseline and do not certify
the refreshed Ollama text corpus above.

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
| Distinct artifact hashes | 49 (44 phrase + 5 image) — six collisions came from byte-identical Argos output |

The counter-run (run1, before the fallback font was declared) is the same matrix
minus the coverage font: 97/100 in 712.8 s, i.e. **3.1× slower end-to-end** —
the retries added latency, and two of the three Russian overlays shipped with
their text effectively missing.

## Acceptance criteria

| Criterion | Result |
|---|---|
| 10 overlays × 10 languages rendered on the production path | **PASS** (Ollama batch 100/100, `vulkan`) |
| Translated text rasterised in the artifact | **PASS** (Ollama batch ink floor 12,364 px; run1/run2 font comparison retained) |
| Same overlay, different text ⇒ different bytes | **PASS** (54 distinct hashes; identical English/Spanish text shares bytes) |
| Output contract 1920×1080 / 24 fps / 120 frames / 5 s / content-address verified | **PASS** (Ollama batch 100/100) |
| Maximum-speed evidence, no estimates | **PASS** (per-frame Chronon sidecars, Vulkan device timestamps, queue and encoder metrics) |
| Published to the target Drive folder, one subfolder per language | **PASS for historical run2 only** (100/100, `evidence/drive_publication.json`); Ollama batch still needs its own publication |

## Remaining gaps (named, not hidden)

- **Native-language review is still required before publication.** Ollama now
  translates each caption from English and automated checks reject empty,
  multi-option, overlong, and known Polish high-speed/resolution errors. This
  does not replace native-speaker review of all 45 outputs.
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
