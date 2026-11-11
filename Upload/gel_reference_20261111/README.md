# GEL Reference 20261111 — 1920×1080, 10 s

Bundle di consegna del look GEL certificato il 7 novembre 2026, ripristinato
nel renderer Vulkan tramite il profilo materiale versionato
`reference_20261107`.

## File

| File | Contenuto |
|---|---|
| `GEL_Reference_20261111_1080p_10s.mp4` | Video completo H.264, 1920×1080, 30 fps, 300 frame, 10.000 s |
| `final.png` | Frame 0 certificato del piano reference |
| `comparison.png` | Confronto reference nuovo vs archivio 7-Nov: abs delta ×4 e title band 2× |
| `final.plan.json` | Piano esatto usato per il render: 7 layer, 3 con `gel_material_profile: reference_20261107` |
| `render.log` | Log del render strict-native Vulkan |
| `sequence-metrics.json` | Metriche della sequenza 0–9 (banda titolo, luma, lit fraction) |
| `EVIDENCE.md` | Report tecnico completo, inclusa la limitazione del golden C ABI legacy |
| `SHA256SUMS.txt` | Hash SHA-256 dei file del bundle |

## Specifiche

- Risoluzione: **1920×1080**
- Frame rate: **30 fps**
- Frame: **300**
- Durata: **10.000 s**
- Codec: **H.264 / yuv420p**, faststart
- Dimensione video: **913.873 byte**
- Piano: `canvas 1920×1080`, `duration_frames 300`, 7 layer
- Profilo: 3 layer testuali con `reference_20261107`; gli altri layer restano invariati

## Provenienza

Il video è stato reso dal piano `final.plan.json` con `chronon3d_cli` e backend
Vulkan in modalità `require_gpu_native`:

```sh
chronon3d_cli render \
  --plan final.plan.json --assets-root <Chronon3d> \
  --backend vulkan --gpu-hot-path-mode require_gpu_native \
  --start-frame 0 --end-frame 299 \
  --video-sink raw --pipe-pixfmt rgba \
  --output gel.rgba

ffmpeg -f rawvideo -pix_fmt rgba -s 1920x1080 -r 30 -i gel.rgba \
  -c:v libx264 -preset slow -crf 16 -pix_fmt yuv420p \
  -movflags +faststart GEL_Reference_20261111_1080p_10s.mp4
```

Il render è stato eseguito con il daemon `chronon3d-c` fermo per evitare
contesa sulla GPU; il servizio è stato riavviato subito dopo. Il raw prodotto
è di **2.488.320.000 byte** (300 × 1920 × 1080 × 4), verificato prima
dell'encoding. Il render è terminato con exit 0 in 2m44s.

## Verifica

- Frame 0 del video vs `final.png`: mean absolute delta **1.2998** (solo
  ricodifica H.264), max 117, 217.966 pixel con delta >2.
- `final.png` vs oracle 7-Nov: mean absolute delta **0.633741**, max 217,
  86.173 pixel con delta >2.
- Sequenza 0–9: title band mean **0.294485–0.294743**, lit fraction
  (`luma > 0.345`) **0.327092–0.327498**.
- Il video H.264 non è un oracle pixel-perfect: l'oracle visivo resta
  `final.png`. Il frame 0 ricodificato serve a verificare che il video non
  sia nero o disallineato.

Il report `EVIDENCE.md` documenta l'unico test C ABI legacy rosso
(`Fingerprint v37 domain golden is pinned`), che riguarda un piano v2
color-only senza `style` e non attraversa il nuovo profilo GEL.

## Drive

| File | Link |
|---|---|
| `GEL_Reference_20261111_1080p_10s.mp4` | [Apri su Drive](https://drive.google.com/file/d/1ebywdWveOaMnmJtJzS1SWFPhGqNftGIb/view?usp=drivesdk) |
| `final.png` | [Apri su Drive](https://drive.google.com/file/d/12gUH5OMwuZH-60z-KICRKJSFb5leaifB/view?usp=drivesdk) |
| `comparison.png` | [Apri su Drive](https://drive.google.com/file/d/1Mm5qcjp-AEc2RRcpoN_nnRUMa57TpUE7/view?usp=drivesdk) |
| `final.plan.json` | [Apri su Drive](https://drive.google.com/file/d/1K2JDZZ4P35Y_umUTwqc6oZYyHp08b1tw/view?usp=drivesdk) |
| `render.log` | [Apri su Drive](https://drive.google.com/file/d/12ZjgcMXHoepJemyuSDTYrPBqdDnsl3Yd/view?usp=drivesdk) |
| `sequence-metrics.json` | [Apri su Drive](https://drive.google.com/file/d/1OAUchNiHQ9wny4VjFEuH4rLHMRLi7cMu/view?usp=drivesdk) |
| `EVIDENCE.md` | [Apri su Drive](https://drive.google.com/file/d/1dvDepjCzxP4-u-IyYPp1WjLGvvr-Fnf1/view?usp=drivesdk) |
