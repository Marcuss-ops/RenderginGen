# CameraRing_Text v3 series — clips 07-12

Correzione ottica della serie v2: ring blur interpolato, tre passate glow,
grana catalog sopra il titolo e micro-drift;
piani RenderPlan V3 generati in C++ da `chronontemplate_emit_camera_ring_text_v3_series`.
Drive (root): https://drive.google.com/drive/u/1/folders/1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS

| # | Frase | Font | Durata | Clip |
|---|---|---|---|---|
| 07 | ogni dettaglio conta | SF-Pro-Display-Semibold.otf | 10s | [apri](https://drive.google.com/file/d/1zkV_rUEYxtSwXEgaajtkEKujVkLsg0ZN/view?usp=drivesdk) |
| 08 | centro nitido, bordi morbidi | DMSans-Bold.ttf | 8s | [apri](https://drive.google.com/file/d/1j2cTFWRZAlAPEhqjo-EnkpdqV8R0NY-T/view?usp=drivesdk) |
| 09 | ogni dettaglio a fuoco | SF-Pro-Display-Bold.otf | 6s | [apri](https://drive.google.com/file/d/11wAwVPUAwQOn_EAlovgwmTI0L8f5F7Te/view?usp=drivesdk) |
| 10 | resta ancora un po’ | PlayfairDisplay-MediumItalic.ttf | 8s | [apri](https://drive.google.com/file/d/1lzpDAmD27y9E_Bi7R2dLcq4vMbDEmVUE/view?usp=drivesdk) |
| 11 | guardala due volte | DMSans-Bold.ttf | 6s | [apri](https://drive.google.com/file/d/1uasaQJnnhubT6bJqe7yvGw_YkxRJuOkL/view?usp=drivesdk) |
| 12 | pulito. preciso. a fuoco. | SF-Pro-Display-Semibold.otf | 7s | [apri](https://drive.google.com/file/d/1_LixDPF9cBbNGVZTCDH5LPsB1jlIYhn2/view?usp=drivesdk) |

## Ricetta

```text
testo 132 px, layering lowercase
camera_ring 64 tap, maschera ellittica 1.85 x 0.72, griglia 8 px interpolata
tre glow testuali (core/mid/atmo), generate.noise catalog, vignette, niente bevel
push-in bezier 0.94 -> 1.012 (frame 1-fine), drift 2 px, fade 12f, tracking 8 -> 0 (24f)
```
