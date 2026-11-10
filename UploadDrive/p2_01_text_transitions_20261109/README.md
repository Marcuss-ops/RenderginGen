# P2-01 text transitions — 1920×1080 delivery

Source: `p2_01_text_transitions_720p.mp4`, 1280×720, 30 fps, 120 frames,
4.000 s, SHA-256
`49a8d95635e689c5b7dd849682c3e3bb0dbaeed3602c146c8ea341013c8243b2`
(this line is informational; the canonical hash is in `upload_manifest.json`).

Delivery: `p2_01_text_transitions_1920x1080.mp4`, 1920×1080, H.264,
30 fps, 120 frames, 4.000 s, CRF 16, Lanczos 1.5× upscale of the source.
It respects the requested 1920×1080 and ≤10 s limit.

## Why the delivery is an upscale

A native 1920×1080 render was attempted with the current software text
renderer. It fails at frame 15, the 90° point of the 3D rotation on
“BEYOND 89”, with:

```
prepare_text_run: invalid or oversized text surface bounds
```

The source 720p render is complete and contains 5,992 non-black pixels at
frame 15 (at 640×360 measurement scale), so this is not missing content. The
native path is a renderer bounds failure at edge-on rotation; the delivered
file is the known-good 720p sequence upscaled with Lanczos. It is **not** a
native 1080p raster.

## Checks on the delivered file

| Check | Result |
|---|---|
| Container / codec | MP4, H.264 |
| Canvas / rate | 1920×1080, 30/1 fps |
| Frames / duration | 120, 4.000 s |
| Empty frames | 0 |
| Non-black pixels | 49,588–123,442 per frame |
| Distinct decoded frames | 111 of 120 (static holds repeat by design) |
| SHA-256 | `d196fb4fbb16d989906e0962b635a6b25dc6e2dd216b095bfaedc2e1955d961e` |

`contact_1920x1080.png` is a 2×2 sheet at frames 0, 40, 80 and 119.
`upload_manifest.json` records the uploaded IDs and hashes.
