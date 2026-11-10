# CameraRing_Text v6 clean — clips 07-12

Bloom HDR nativo: piramide 6 mip RGBA16F, prefilter Karis, down 13 tap,
up 9 tap, composito additivo; ACES ed esposizione 1.0 al confine di output.
Piani RenderPlan V3 generati in C++ da `chronontemplate_emit_camera_ring_text_v6_clean`.
Drive (root): https://drive.google.com/drive/u/1/folders/1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS

| # | Frase | Font | Durata | Clip |
|---|---|---|---|---|
| 07 | ogni dettaglio conta | SF-Pro-Display-Semibold.otf | 10s | [apri](https://drive.google.com/file/d/1xwdoXPLk3xmTTe6DHDADeraRCPapaK6E/view?usp=drivesdk) |
| 08 | centro nitido, bordi morbidi | DMSans-Bold.ttf | 8s | [apri](https://drive.google.com/file/d/1aSblbGL64iK47lPssc9GZWIls1Xlh1-T/view?usp=drivesdk) |
| 09 | ogni dettaglio a fuoco | SF-Pro-Display-Bold.otf | 6s | [apri](https://drive.google.com/file/d/1-8qk0JMLpfMhizkHwJgFbXAn2Zn12gH4/view?usp=drivesdk) |
| 10 | resta ancora un po’ | PlayfairDisplay-MediumItalic.ttf | 8s | [apri](https://drive.google.com/file/d/1pElS2LItuaH1SZi0O0lRJIktU5fUnghg/view?usp=drivesdk) |
| 11 | guardala due volte | DMSans-Bold.ttf | 6s | [apri](https://drive.google.com/file/d/1qpaTop2Qz24p1c9AaGCU1DRlek-JUNWm/view?usp=drivesdk) |
| 12 | pulito. preciso. a fuoco. | SF-Pro-Display-Semibold.otf | 7s | [apri](https://drive.google.com/file/d/12MZvXJqfyXURGg6dbI67R8hPIiaiuCbu/view?usp=drivesdk) |

## Ricetta

```text
testo 132 px, layering lowercase
camera_ring 64 tap, maschera ellittica 1.85 x 0.72, griglia 8 px interpolata
bloom nativo 6 mip RGBA16F, threshold 0.52, intensity dimezzata, additivo
camera ring 0.09/0.64, ACES + exposure 1.0, generate.noise, vignette
push-in bezier 0.94 -> 1.012 (frame 1-fine), drift 2 px, fade 12f, tracking 8 -> 0 (24f)
```
