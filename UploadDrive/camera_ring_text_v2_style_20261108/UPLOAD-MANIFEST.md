# CameraRing_Text v2 series — clips 07-12

Stessa ricetta dei riferimenti `CameraRing_Text_01..06_..._v2`;
piani RenderPlan V3 generati in C++ da `chronontemplate_emit_camera_ring_text_v2_series`.
Drive (root): https://drive.google.com/drive/u/1/folders/1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS

| # | Frase | Font | Durata | Clip |
|---|---|---|---|---|
| 07 | ogni dettaglio conta | SF-Pro-Display-Semibold.otf | 10s | [apri](https://drive.google.com/file/d/1dlbLr1NH-d53eqjPTPJcPII5O0ns8pTm/view?usp=drivesdk) |
| 08 | centro nitido, bordi morbidi | DMSans-Bold.ttf | 8s | [apri](https://drive.google.com/file/d/1slHkevj-xY2JULPHXjSoZ9WLl-ScBf9c/view?usp=drivesdk) |
| 09 | ogni dettaglio a fuoco | SF-Pro-Display-Bold.otf | 6s | [apri](https://drive.google.com/file/d/1DtAJmgo85XRfnI9IDyepqjcEarVn_mov/view?usp=drivesdk) |
| 10 | resta ancora un po' | PlayfairDisplay-MediumItalic.ttf | 8s | [apri](https://drive.google.com/file/d/1m1xW9dkBENKvsNGULVsZfnuwalrD77y2/view?usp=drivesdk) |
| 11 | guardala due volte | DMSans-Bold.ttf | 6s | [apri](https://drive.google.com/file/d/1Ss1OgU_PSKujtYzAosvc3tNMhB6i4taY/view?usp=drivesdk) |
| 12 | pulito. preciso. a fuoco. | SF-Pro-Display-Semibold.otf | 7s | [apri](https://drive.google.com/file/d/1uCfrWEfZgTEsKm0e_SkHM8rXPx7SE9rT/view?usp=drivesdk) |

## Ricetta

```text
testo 132 px, layering lowercase
camera_ring 64 tap, maschera ellittica 1.85 x 0.72
light.sdf_bevel, generate.noise, vignette
push-in bezier 0.94 -> 1.0 (frame 1-91), fade 12f, tracking 8 -> 0 (24f)
```
