# TICKET — Workaround cross-repo da chiudere (2026-11-17)

Stato: **CHIUSO (2026-11-22)**. I tre workaround residui sono stati rimossi:
ModernMap1 usa il trim operator nativo, il contratto rifiuta `transcode` e il
worker non accetta più URL in `logical_path`. Il quarto (discovery dei build
sibling) era già stato ritirato il 2026-11-21. Le sezioni 1–3 restano come
traccia storica del problema e del criterio di rimozione; l'esito verificato è
in fondo.

## 1. ModernMap1: pre-clip delle route al posto del trim nativo

- **Evidenza:** `renderinggen/internal/overlay/semantic_map_modern.go`
  (`trimMapPolyline` e il commento "the native Vulkan shape path ignores an
  operator-level trim animation").
- **Cosa fa il worker:** taglia la polilinea proiettata in base a
  `trim_start`/`trim_end` prima di emettere il layer, perché il renderer
  disegna il range seed e ignora l'animazione di trim.
- **Proprietario:** Chronon3d (shape path trim).
- **Criterio di rimozione:** un render plan con `trim` animato produce il range
  richiesto su Vulkan; aggiungere un test di parità; poi eliminare
  `trimMapPolyline` e passare i valori al renderer.
- **Verifica:** piano ModernMap1 con route e trim 0.25..0.75, frame di
  controllo prima/dopo il taglio.

## 2. Audio: `transcode` richiesto, `copy` prodotto

- **Evidenza:** `renderinggen/internal/processor/render_selection.go`
  (`audioModeCopyOnly`, `warnInert`) e `prepare_stages.go`
  (`AudioInertParams`, warning).
- **Cosa fa il worker:** accetta `mode=transcode` e i parametri
  codec/sample_rate/channels, poi copia la traccia sorgente e registra un
  warning. L'artifact non corrisponde al contratto richiesto.
- **Proprietario:** PipelineGen/Chronon (contratto audio e mux).
- **Criterio di rimozione:** o il mux implementa il transcode, o il contratto
  rifiuta i modi non-copy in compilazione. Nel secondo caso aggiornare
  PipelineGen e aggiungere un test fail-closed.
- **Verifica:** piano con audio 44.1 kHz su sorgente 48 kHz e
  `mode=transcode`; `ffprobe` deve mostrare il sample rate richiesto, oppure il
  compile deve fallire con un errore esplicito.

## 3. Asset: URL dentro `logical_path`

- **Evidenza:** `renderinggen/internal/processor/materialize.go`
  (`assetSourceURL`, "LEGACY self-heal") e `asset_merge.go`.
- **Cosa fa il worker:** se `logical_path` è `http(s)`, lo tratta come
  `source_url`, scarica (hash verificato, cap 1 GiB) e logga. È un residuo del
  contratto precedente.
- **Proprietario:** PipelineGen (producer degli asset).
- **Criterio di rimozione:** nessun producer emette URL in `logical_path` per
  una finestra di migrazione concordata; poi il worker rifiuta lo schema e
  richiede `source_url`.
- **Verifica:** job di replay con manifest vecchio (deve continuare a funzionare
  fino alla data) e job nuovo senza `source_url` (deve fallire).

## 4. Minore: discovery dei build sibling — RISOLTO 2026-11-21

- **Evidenza:** `renderinggen/internal/tacticalfill/tacticalfill.go`
  (`../../ChrononTemplate/build/...`) e i comandi `date-animation` /
  `map-tactical-fill` (`../../Chronon3d/build/...`).
- **Cosa faceva:** cercava un emitter/binario in path relativi alla CWD.
- **Rimozione eseguita:** `tacticalfill.ResolveEmitter` ora restituisce solo il
  path esplicito o `CHRONONTEMPLATE_EMIT_TACTICAL_FILL`; la discovery dei build
  sibling vive in `cmd/map-tactical-fill` (`resolveEmitter`). `date-animation`
  ed `editorial-map` avevano già la discovery nel proprio `cmd/`.
- **Verifica:** `go build ./...`, `go test ./internal/tacticalfill
  ./cmd/map-tactical-fill -count=1` verdi; la libreria non contiene più path
  relativi `../../ChrononTemplate/build/...`.

## Verifica 2026-11-21 — tre workaround restano, il quarto è ritirato

Ricontrollo sul tree `RenderingGen@c515fb5` del 2026-11-21: i primi tre punti
sono ancora presenti nel codice e nessun proprietario ha chiuso il criterio di
rimozione. La discovery dei build sibling (#4) è stata spostata nei `cmd/` e
la libreria non la contiene più. Il working tree ha modifiche concorrenti su
`config.yaml` e `internal/overlay/chain_segments.go`; nessuna tocca i simboli
qui sotto.

| # | Simbolo cercato | Esito |
|---|---|---|
| 1 | `trimMapPolyline` (`internal/overlay/semantic_map_modern.go:263`, definizione in `semantic_map_modern_geometry.go:19`) | presente |
| 2 | `audioModeCopyOnly` (`internal/processor/render_selection.go:78`, usato in `prepare_stages.go:432`) | presente |
| 3 | `assetSourceURL` (`internal/processor/materialize.go:221`, chiamato a `:128`) | presente |
| 4 | discovery `../../ChrononTemplate/build/...` | **rimosso dalla libreria**; vive in `cmd/map-tactical-fill` |

Il ticket è stato chiuso il **2026-11-22**: i tre punti sono stati rimossi e
verificati con i test elencati nell'Esito. La verifica pixel su Vulkan del punto
1 è stata rieseguita lo stesso giorno sul binario release con il sink raw
pre-encode: primo estremo 0 pixel verdi, midpoint 384, ultimo estremo 0. Dettaglio
e hash in `RenderingGen/evidence/modernmap1-trim-vulkan-20261122/`
(`pixel-check.json`, frame raw e PNG). Il gate resta opt-in: senza `CHRONON_BIN`
il test si salta, non fallisce.

## Esito 2026-11-22 — tre workaround rimossi

### 1. ModernMap1: trim nativo, niente pre-clip

- `semantic_map_modern.go` emette `LayerPathOperator{Kind:"trim"}` sul path
  completo; `trimMapPolyline`/`mapPolylinePoint` sono stati rimossi da
  `semantic_map_modern_geometry.go`.
- Il compilatore nativo applica il trim ai path shape normali:
  `Chronon3d/src/render_plan/render_plan_compiler_shape_path.cpp` (Trim →
  `PathOperatorKind::Trim`, valutato per frame) e il dispatch Vulkan usa
  `path.stroke.trim_start/trim_end`
  (`Chronon3d/src/backends/vulkan/vulkan_native_shape_dispatch_path.cpp`).
- Test plan-level: `TestModernMap1RouteKeepsFullGeometryWithNativeTrim`
  verifica che la polilinea non sia pre-clippata e che l'operatore statico
  [0.40,0.60] sia nel piano. `go test ./internal/overlay -run TestModernMap1
  -count=1` verde.
- Test pixel: `TestModernMap1RouteTrimRendersOnVulkan` renderizza un frame
  ModernMap1 sul backend Vulkan nel sink raw pre-encode (`--video-sink raw
  --pipe-pixfmt rgba --encoder-backend pipe`) e verifica il trim statico
  [0.40,0.60]: 0 pixel verdi sui due estremi, 384 nel midpoint, 908 pixel
  verdi nel bbox [826,289]-[884,367], coerente con il range d'arco
  0.40..0.60. Il layer route è traslato da [-960,-540] a [0,0] solo per il
  frame raw, perché il path primitive usa `position` come traslazione diretta
  e la posizione di produzione cadrebbe fuori canvas; geometria e operatore
  restano invariati. Evidenza:
  `RenderingGen/evidence/modernmap1-trim-vulkan-20261122/`
  (`pixel-check.json`, `modernmap-trim.raw`, `modernmap-trim.png`,
  `render.log`). Il lane PNG non è pixel-truth; il gate usa il raw.
- Probe supplementare animato: un path 512×256 con trim animato 0→1 su 10
  frame produce al frame 5 pixel verdi solo da x=64 a x=255 (50% iniziale) e
  nessun pixel dopo; raw e PNG in
  `supplemental-animated-trim-frame5.*`.

### 2. Audio: il contratto rifiuta `transcode`

- `RenderRequest.Validate`, `CompileInput` e `ClipRenderPlanV1.Validate`
  rifiutano `AudioModeTranscode`; `defaultContractResolver.Resolve` fa lo stesso
  prima di risolvere il contratto.
- Il worker ha `validateAudioMode` in
  `internal/processor/render_selection.go` e fallisce in `prepareCompile`: un
  piano vecchio o estero con `transcode` non viene copiato silenziosamente.
- Test: `TestCopyModeDoesNotPinAudioBitrateAndTranscodeIsRejected`,
  `TestValidate_AudioAndTranscriptEnums`, `TestCompile_FailClosedMissingInputs`,
  `TestValidateAudioModeRejectsTranscode`; suite
  `go test ./internal/capabilities/cliprender` e `go test ./...` in
  `RenderingGen/renderinggen` verdi.

### 3. Asset: URL solo in `source_url`

- `prepareAssets` (`render_prepare_queue.go`) usa `semanticAssetLogicalPath` per
  `logical_path` e mette l'eventuale URL del provider in `source_url`.
- Il worker rifiuta un URL in `logical_path` in `assetSourceURL`
  (`materialize.go`) e in `mergeAssets` (`asset_merge.go`); il merge non
  converte più un manifest URL-backed, fallisce chiuso.
- Test: `TestPrepareRejectsURLInLogicalPath`,
  `TestMergeAssetsRejectsURLBackedManifest`; suite completa
  `go test ./internal/processor` e
  `go test ./internal/capabilities/scripts` verdi.

### Verifica eseguita

```
cd RenderingGen/renderinggen && CHRONON_BIN=<...>/chronon3d_cli \
  RENDERINGGEN_MODERNMAP_TRIM_RENDER=1 go test ./internal/overlay \
  -run 'TestModernMap1RouteKeepsFullGeometryWithNativeTrim|TestModernMap1RouteTrimRendersOnVulkan' -count=1 -v  # PASS
cd RenderingGen/renderinggen && go test ./... -count=1                         # PASS
cd refactored && go test ./internal/capabilities/cliprender -count=1           # PASS
cd refactored && go test ./internal/capabilities/scripts ./internal/platform/renderinggen -count=1  # PASS
cd RenderingGen/renderinggen && go build ./...                                  # PASS
cd refactored && go build ./...                                                 # PASS
```
