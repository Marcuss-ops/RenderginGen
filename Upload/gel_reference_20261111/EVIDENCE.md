# GEL reference mode — 2026-11-11

## Result

`GelCinematicTitlePack` exposes an opt-in `Reference20261107` look that
reproduces the 2026-11-07 seven-layer closure through the normal
`ChrononTemplate → RenderPlan → Chronon3D Vulkan` path:

| # | layer | material / placement |
|---|---|---|
| 1 | plate | full-frame screen-space image |
| 2–4 | three extruded backings | `#1454FF`, depth 8, `generate.fill`, scale `1.004×`, offset `+1/+5 px` |
| 5–7 | three GEL faces | `#00E8FF`, opaque (`gel_transmission: false`), pinned horizontal anamorphic streak |

The backings are authored before the faces, so the face body stays readable
while the royal-blue rim remains visible on the lower/right edge. The current
`CinematicV1` recipe remains the default and is unchanged.

Reference mode pins the optics of the 7-Nov plan: opaque faces, no downward
light shafts, and `light.lens_flare` at `threshold 0.88`, `intensity 0.55`,
`radius 0.98`, `halo 0.02`, `ghost 0`, `streak 1`.

### Renderer material profile

The renderer now carries a versioned `gel_material_profile`:

| value | behaviour |
|---|---|
| `current` (default) | post-2026-11-08 shading, unchanged |
| `reference_20261107` | certified 2026-11-07 constants from commit `fdbdf2985` |

The profile is part of the RenderPlan schema v2/v3, `TextMaterial`, the text-run
hash, the direct and fused `effect_flags` packing (bit 13), and the Vulkan
`text_tile_raster.comp` shader. The reference branch restores the razor-tight
1.25-texel bevel, the two-stop cyan body (`(0.00,0.28,0.58)` →
`(0.00,0.90,1.00)`), the white specular `(0.88,1.00,0.94)` at
`pow(ndoth,40) * 4.5`, green caustics, the cyan/green rim, and removes the lime
crest and pale wash. The non-reference path is byte-for-byte the previous
shading.

## Verification

- `chronontemplate_gel_cinematic_title_test`: **25 checks, 0 failures**
  (`build/dev-fast`, built with `-j8`).
- Plan emission: **7 layers**. Structural diff against the archived
  `revision_wing.plan.json` is exactly **15 fields**: 12 lens-flare parameters
  (the deliberately pinned `0.88/0.55/0.98` vs the archive's
  `0.55/1.10/0.18`) and 3 `gel_material_profile` fields. Every layer, geometry,
  fill, effect order and transform calendar is identical.
- Vulkan frame: **PASS**, strict-native (`require_gpu_native`), 1920×1080,
  `reference_20261107` plan. Against the certified
  `gel-cinematic-title-1080p-revision-20261107/D_full_wing_final.png`:
  mean absolute delta **0.633741**, max **217**, **86,173** pixels above 2 RGB
  levels. Before the profile the same comparison measured mean **1.4889**,
  **110,054** pixels >2. The material drift is therefore materially reduced,
  not hidden by a preset change.
- Ten-frame sequence (0–9): 10/10 frames written, no dropped layer. Title band
  rows 460–635, Rec.709 luma: mean **0.294485–0.294743**, lit fraction
  (`luma > 0.345`) **0.327092–0.327498**; see `sequence-metrics.json` for the
  per-frame values and the metric definition.
- `chronon3d_c_abi_tests`: **473/474 cases, 10,274/10,275 assertions**;
  the only failure is the legacy `Fingerprint v37 domain golden is pinned`
  (`11656075431140586116` actual vs `11091242584077091452` pinned). The legacy
  v2 plan is a color layer with no `style`, so the new profile is not on that
  path; the GEL filter (`*gel*`) passes **2/2 cases, 48/48 assertions**.
- `chronon3d_renderer_core_tests`: **211/211 cases, 72,476/72,476 assertions,
  PASS**.
- `chronon3d_text_tile_raster_canary_tests`: **1/1 case, 154/154 assertions,
  PASS**.

Command used for the frame:

```sh
build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli render \
  --plan docs/evidence/gel-reference-mode-20261111/final.plan.json \
  --assets-root "$PWD" --backend vulkan \
  --gpu-hot-path-mode require_gpu_native \
  --start-frame 0 --end-frame 0 --video-sink raw --pipe-pixfmt rgba \
  --output /tmp/gel-reference-mode-20261111.rgba
python3 tools/raw_frame_to_png.py \
  --raw /tmp/gel-reference-mode-20261111.rgba \
  --width 1920 --height 1080 \
  --out docs/evidence/gel-reference-mode-20261111/final.png
```

The sequence used the same command with `--start-frame 0 --end-frame 9` and
`--output /tmp/gel-reference-mode-20261111-seq.rgba`; the metric is the mean and
the `>0.345` fraction over rows 460–635 of each frame.

## Residual difference

The plan diff shows that the only non-profile difference from the archived plan
is the 12 lens-flare parameters. The residual **0.6337** against the archived
composite is therefore expected: the new plan deliberately pins the certified
7-Nov flare (`0.88/0.55/0.98`), while the archived `revision_wing.plan.json`
uses the legacy `0.55/1.10/0.18`. A pixel-exact archive match would require the
legacy flare, which is not the certified 7-Nov look. No second material term is
left to restore in the GEL body.

The 1094×143 `gel-final-20261107` folder is **not a usable pixel oracle**: its
README records `final.plan.json` SHA-256 `5393581f…`, but the committed file is
`1f1dcf92…`; it records `final.png` SHA-256 `c8636d6d…`, but the committed PNG
is `ae4787bd…`. Both the current CLI and the 2026-11-06 prebuilt CLI differ from
that PNG, so the folder cannot establish a renderer regression on its own.

## Artifacts

| file | bytes | SHA-256 |
|---|---:|---|
| `final.plan.json` | 750359 | `35ea5a71fbf1e89e667ea6ed4d9f70edfa7d3a7fcb042137dc6a6d691ed2d28e` |
| `final.png` | 1125854 | `64b694a9fff7df23b9104a396ca0acb1718e69a8bcae5f953590b414865ac9a8` |
| `comparison.png` | 585138 | `a95820a4ba6debcc90eb09da48bb2a776f16f55383368009d9aaf69f5af85713` |
| `render.log` | 10715 | `206f09c12775e29b53db8a608db6f4ec5f499380bdb280aac85a88a9013ba907` |
| `sequence-metrics.json` | 1144 | `321487777a648b90433ba9616d6b3c1b3f84a3a5bec4915f9e4d5cc34bf5cf4e` |
