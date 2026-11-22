# ModernMap1 — gate pixel del trim nativo su Vulkan (2026-11-22)

Ticket: `TICKET-WORKAROUNDS-2026-11-17` #1. Esito: **PASS**.

## Comando

```sh
cd RenderingGen/renderinggen
CHRONON_BIN=<Chronon3d>/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli \
RENDERINGGEN_MODERNMAP_TRIM_RENDER=1 \
RENDERINGGEN_TRIM_EVIDENCE_DIR="$PWD/../evidence/modernmap1-trim-vulkan-20261122" \
go test ./internal/overlay \
  -run 'TestModernMap1RouteKeepsFullGeometryWithNativeTrim|TestModernMap1RouteTrimRendersOnVulkan' \
  -count=1 -v
```

Risultato della run registrata:

```text
--- PASS: TestModernMap1RouteKeepsFullGeometryWithNativeTrim (0.45s)
--- PASS: TestModernMap1RouteTrimRendersOnVulkan (6.45s)
    raw trim gate: first=(956.1,557.1,0 green) mid=(872.4,320.1,384 green) last=(642.9,272.7,0 green)
```

## Cosa misura il gate principale

- Piano `chronon.render-plan.v3`, layer
  `shape-map:modernmap1:route:trimmed`, 5 stop, polilinea completa di 6 punti.
- Operatore nativo `trim` con `start=0.40`, `end=0.60` (nessuna animazione).
- Backend Vulkan, frame 0, superficie **raw RGBA8 pre-encode**
  (`--video-sink raw --pipe-pixfmt rgba --encoder-backend pipe`), un frame
  1920×1080 = 8.294.400 byte.
- Pixel verdi: 908, bbox `[826,289]-[884,367]`. Il midpoint al 50% dell'arco
  `(872.4,320.1)` ha 384 pixel verdi; il primo `(956.1,557.1)` e l'ultimo
  `(642.9,272.7)` ne hanno 0. Il bbox è coerente con il range d'arco
  `[0.40,0.60]`.
- Il test plan-level certifica che il piano di produzione conserva la
  polilinea completa e l'operatore `trim` nativo.

Nota di onestà: per il solo frame raw il layer route è traslato da
`[-960,-540]` a `[0,0]`, perché il path primitive usa `position` come
traslazione diretta e la posizione “centered map” di produzione cadrebbe fuori
canvas. Geometria del path e parametri del trim restano invariati.

## Perché il sink raw

Il lane PNG/still è dichiarato dal motore non-pixel-truth
(`utils/job/output_lane_pixel_truth.hpp`): non compone gli shape SVG e non è
una superficie di verità per frame/backend. Il sink raw è la verità
pre-encode canonica; il PNG in questa directory è solo una conversione del raw
per ispezione umana.

## Probe supplementare: trim animato

`supplemental-animated-trim-frame5.raw` è un path 512×256 con trim animato
`0→1` sui frame 0..10. Al frame 5 (50%) i pixel verdi sono solo da `x=64` a
`x=255` (192 pixel), nessuno da `x=256` in poi. Questo prova che il trim
animato viene valutato per frame su Vulkan.

## File e hash

| File | Contenuto |
|---|---|
| `modernmap-trim.plan.json` | piano compilato usato dal gate |
| `modernmap-trim.raw` | frame RGBA8 1920×1080 (8.294.400 byte) |
| `modernmap-trim.png` | conversione del raw per ispezione |
| `render.log` | log completo del render Vulkan/raw |
| `pixel-check.json` | misure, coordinate e SHA-256 di tutti gli artefatti |
| `supplemental-animated-trim-frame5.raw` | frame 5 del probe animato |
| `supplemental-animated-trim-frame5.png` | conversione del probe animato |
| `supplemental-animated-trim.plan.json` | piano del probe animato |
| `supplemental-animated-trim-render.log` | log del probe animato |

Hash principali (tutti in `pixel-check.json`):

- `modernmap-trim.raw`:
  `212441f3eac5384370a973167788318dd6d4106708a23514bb1c517a5e7a673c`
- `modernmap-trim.png`:
  `03ec1181ed88ee58486709eb04b392384ba743e2108efea59f1a8cff77448771`
- `supplemental-animated-trim-frame5.raw`:
  `83e48a7c6e4cc8720756f5d10690f6ffe8824973890135aef6de9f4fbbab627a`
