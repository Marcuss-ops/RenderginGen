# Text parity T0-01 — 1920×1080 demo bundle (2026-11-10)

Eight clips, one per frozen T0-01 corpus case, rendered at 1920×1080 / 30 fps
through the strict native Vulkan MTSDF route. `latin_basic` and `fractional`
also ship a software-reference clip because they exercise the subpixel
placement fix.

## What changed

The software text rasterizer rasterizes into a tight scratch surface and then
composites it with a translation fast path that blits at `round(offset)`. The
surface origin was fractional (`min_x - 8`), so every run was shifted by up to
half a pixel. The origin is now floored to an integer pixel and the surface is
sized from that origin; glyphs keep their fractional position *inside* the
surface.

Measured on the frozen `latin_basic` gate (CLI rebuilt after the fix):

| Metric | Before | After |
|---|---:|---:|
| alpha IoU | 0.947735 | 0.985900 |
| RGB MAE | 0.020837 | 0.002934 |
| RGB p99 | 0.517647 | 0.082353 |
| SSIM | 0.982458 | 0.999306 |
| PSNR | 21.03 dB | 35.06 dB |
| bbox | 1 px diff | identical |

The remaining gate failure is the antialias/coverage reconstruction on curved
edges, not placement. The `effects`/`multi_shadow` canonical shadow and the GPU
`VectorStroke` remain open (T1-01/T1-02); `text_transitions` + spans remains
fail-closed (T2-01).

## Clips

| # | Case | Frames | Duration | Backends |
|---|---|---:|---:|---|
| 01 | `latin_basic` | 90 | 3 s | Vulkan + software |
| 02 | `exact_3d` | 120 | 4 s | Vulkan |
| 03 | `effects` | 61 | 2 s | Vulkan |
| 04 | `multi_shadow` | 90 | 3 s | Vulkan |
| 05 | `rtl_caret` | 90 | 3 s | Vulkan |
| 06 | `transitions` | 61 | 2 s | Vulkan |
| 07 | `fractional` | 90 | 3 s | Vulkan + software |
| 08 | `multipage` | 90 | 3 s | Vulkan |

## Reproduce

```bash
python3 RenderingGen/UploadDrive/text_parity_20261110/make_plans.py
python3 RenderingGen/UploadDrive/text_parity_20261110/render_bundle.py
VELOX_CONFIG_DIR="$HOME/.config/velox" \
  python3 RenderingGen/UploadDrive/text_parity_20261110/upload_drive.py
```

Render command per case: `chronon3d_cli render --plan <plan> --assets-root
Chronon3d --backend vulkan --gpu-hot-path-mode require_gpu_native --profile
production --tone-map aces --exposure 1.0 -o videos/<clip>.mp4`. The software
comparison uses `--backend software` without the GPU mode flag.

## Delivery

Parent Drive folder: `1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS`
Subfolder: `Text_Parity_Subpixel_20261110`
