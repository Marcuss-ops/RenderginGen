# Overlay batches — golden v3.1 maps, 2026-11-11

Job: `job_1794389213628931358_17647fa0`
Run: `run_46f35993-e5ce-47d7-bbbd-93476a43b9c4`
Payload: `overlay-throughput-golden-100-overlay-only-v3.1-maps-noentity`

## Cosa contiene

- **38 overlay validati**, tutti 1920×1080 / 24 fps, durata massima 8,0 s.
- 4 entity card testuali, 10 numeri/date, 2 mappe, 22 frasi.
- 17 batch per scena (solo scene con overlay validi) e 4 batch per classe.
- Contact sheet per classe e contact sheet delle eccezioni.
- `MANIFEST.json` con SHA-256, durata, item ID e motivazione delle esclusioni.

Le entity card di questo run sono **solo testo**: `entity_images.enabled=false`
perché il ritratto di Andrea Sempio aveva il sorgente Drive cancellato (404).
Non ci sono quindi cornici/foto entità da valutare; i testi sono centrati sul
canvas bianco e le mappe non hanno bordi o ritagli strani.

## Esclusioni (6)

| # | Motivo |
|---|---|
| 14 | `text_3d_tilt_rise`: tutti i frame campionati sono bianchi |
| 26 | `text_3d_roll_depth`: tutti i frame campionati sono bianchi |
| 35 | `text_3d_camera_push`: tutti i frame campionati sono bianchi |
| 39 | entity card da echo del prompt: “Overlay Check” |
| 43 | entity card da echo del prompt: “Scheda Entità Overlay” |
| 44 | entity card da echo del prompt: “Mappa Pertinente Andrea Sempio” |

I tre testi 3D sono un difetto del renderer su fondo bianco, non un fade: il
frame è bianco a 0,5/1,7/3,1/4,3 s (e analogamente per gli altri due). La
selezione automatica delle frasi lunghe ora esclude `text_3d_*`; la famiglia
resta nel catalogo per un uso esplicito solo dopo una certificazione pixel su
fondo bianco. Le tre entity card sono state filtrate in
`projectEntityAnnotations` perché sono etichette/istruzioni, non entità.

## Drive

Parent: `1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS`
Subfolder: `Overlay_Batches_20261111`

Caricamento:

```bash
VELOX_CONFIG_DIR="$HOME/.config/velox" \
  python3 RenderingGen/UploadDrive/overlay_batches_20261111/upload_drive.py
```
