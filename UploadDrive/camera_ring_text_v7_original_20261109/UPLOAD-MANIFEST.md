# CameraRing_Text v7 original — clips 13-15 (manifest v2)

Bloom HDR nativo: piramide 6 mip RGBA16F, ACES ed esposizione 1.0 al confine di output.
Piani RenderPlan V3 derivati dalla ricetta v6 clean, con tre frasi inedite.
Drive (root): https://drive.google.com/drive/u/1/folders/1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS

> v2 corregge il report v1: la route `mtsdf` è letta dal render log e gli still sono elencati.
> Il manifest v1 resta superseded.

| # | Frase | Font | Durata | SHA-256 | Clip |
|---|---|---|---|---|---|
| 13 | il dettaglio è tutto | SF-Pro-Display-Semibold.otf | 10s | fbf8797c151d… | [apri](https://drive.google.com/file/d/11zH1qcojrhdhON5maD9xOf-ucIW5yjR0/view?usp=drivesdk) |
| 14 | la luce che resta | PlayfairDisplay-MediumItalic.ttf | 8s | c305ad106a9a… | [apri](https://drive.google.com/file/d/1CfZdUyQWTbEo3rbepfu9FKLPTcwoKpJ-/view?usp=drivesdk) |
| 15 | fermati un istante | DMSans-Bold.ttf | 6s | 34c32faef3dd… | [apri](https://drive.google.com/file/d/1c4rw3K2DHxnh2USm07fgJ7kyMuDqMY6d/view?usp=drivesdk) |

## Verifica

- Tutte le clip: **1920×1080**, **30 fps**, H.264 `yuv420p`.
- Frame: 300 / 240 / 180; durate 10 / 8 / 6 s.
- Route testo: `mtsdf` su tutte le clip.
- SHA-256 locale = SHA-256 remoto per ogni file.
- Still early/mid/late presenti per ogni clip.
- `verification.all_pass`: **true**.

## Ricetta

```text
testo 132 px, layering lowercase
camera_ring 64 tap, maschera v6 clean, blur 0.020-0.042
bloom nativo 6 mip RGBA16F, threshold 0.52, intensity 0.28-0.45
ACES + exposure 1.0, generate.noise, vignette
push-in bezier 0.94 -> 1.012, drift 2 px, fade 12f, tracking 8 -> 0 (24f)
```
