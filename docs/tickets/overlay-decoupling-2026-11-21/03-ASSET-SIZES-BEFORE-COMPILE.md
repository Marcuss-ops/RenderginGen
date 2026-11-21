# OVL-03 — Dimensioni asset prima del compile: zero mutazioni post-compile

**Stato:** PASS · **Priorità:** P0

## Evidenza iniziale

- `overlay` compila al buio e marca i layer con metadati transitori
  (`EntityImage`, `DeriveWidthFromAsset`, `Premium*`).
- `processor/entity_image_geometry.go` apriva i file materializzati e ri-mutava
  box, `Size`, mask, componenti premium e caption; `prepare_stages.go` doveva poi
  ricostruire il prepared package (`packageStale`).

## Esito

### Overlay

- `overlay.CompileSemanticWithAssetSizes(raw, sizes)` → `compileSemanticDecoded`
  salva la tabella su `semanticPlan.assetSizes`. `CompileSemantic` resta la
  modalità blind (sizes nil) per demo, tool e prepared package legacy.
- `applyProbedImageGeometry` viene applicata **dentro** il lowering di ogni
  immagine, prima di premium e caption (`compileSingleImageLayerResolved`,
  `compileEntityCard`); un layer che richiede la size e non la trova fallisce
  chiuso (`probedImageSize`).
- La caption nata da un'immagine adattata viene pinnata con
  `pinCaptionFitSize` (`Min = Max = FontSize`, `FitModeShrinkOnly`): Chronon
  rifiuta i limiti di font senza un fit mode non-none. Il pin ora avviene nello
  stesso compile che ha risolto la geometria, non in un pass successivo.
- `ChainPanOffset` resta solo traslazione della strip. Deviazione motivata dal
  target: nella camera-pan la caption è testo screen-space **non linkato**
  (`CaptionForImageID == ""`), quindi il fit avviene dentro
  `compilePanChain` **dopo** il piazzamento della strip, replicando esattamente
  la geometria spedita (stessa finestra e stessa posizione). Se il fit girasse
  prima, la caption si sposterebbe: cambio visivo non richiesto. Il processor
  non muta nulla in entrambi i casi.

### Processor

- `entity_image_geometry.go` ora contiene solo `probePlanImageSizes` +
  `entityImageDimensions`: apre i byte verificati e restituisce la tabella
  `map[string]ImageSize`; nessuna scrittura sul piano.
- `prepareWorkspace`: materialize → probe → **pass B** `compileSemanticWithAssetSizes`
  → `normalizeMaterializedImagePaths` → package. Lo stato (`plan`, `result`,
  `stats`, `preparedPackage`, `compiledAssets`) viene sostituito dal pass B; il
  piano del pass A è scartato.
- `materialize.Prepare` (prepare-only): stessa sequenza, il piano salvato è
  quello del pass B.
- `fitEntityImageLayersToAssets` eliminato; `packageStale` non viene più marcato
  per il fit (solo rename di estensione e burn dei sottotitoli).
- Il processor non nomina più `Premium*`, `ChainPanOffset` né i marker
  transitori.

### Costo dichiarato

Un lowering in più per job. Il pass A serve solo a conoscere gli asset ref; il
probe è `image.DecodeConfig` sugli stessi byte già materializzati (nessun
download, nessuna decodifica completa). Il prepared package è costruito una
sola volta, dal piano finale.

## Criteri di accettazione

1. **PASS** — `TestPrepareJobShipsSettledGeometry`: dopo `PrepareJob` nessun
   layer porta marker, e rieseguire `FitImageLayersToSizes` sul piano prepared
   restituisce `changed == false`.
2. **PASS** — `entity_image_geometry.go` non contiene più il fit; grep del
   processor su `Premium`, `ChainPanOffset`, `fitEntityImageLayersToAssets` è
   vuoto.
3. **PASS** — `TestSizedCompileMatchesThePostCompileFit` (portrait 1600×900,
   caption e componenti premium: stesso `Size`, box, fit, posizione e stile).
4. **PASS** — `TestSizedCompileMatchesThePostFitOnACameraPanChain`: tre segment
   camera-pan con caption, confronto JSON completo tra sized compile e
   compile+fit (finestra, posizione, `ChainPanOffset`, animazione).
5. **PASS** — `TestSizedCompileMatchesThePostFitOnACompositeGroup`: legacy
   composite replay con due member, caption e aspect opposti; il box adattato
   resta identico al post-fit anche dopo premium e caption.
6. **PASS** — `TestSizedCompileMatchesThePostFitOnACameraSequence`: sequence
   camera con viewport e caption screen-space; il probe non altera box,
   posizioni o stile caption.
7. **PASS** — `TestSizedCompileFailsClosedWithoutAProbe` e
   `TestProbePlanImageSizesFailsClosed`.
8. **PASS** — suite overlay + processor + chronon + workspace + renderbatch
   verde e `go test ./...` exit 0 (vedi Verifica).
9. **PASS** — i piani sized passano il validator reale di Chronon:
   `TestCompiledMultiImagePlansPassChrononValidation` copre entity card,
   camera-pan chain, composite group e camera sequence, tutti `valid: true`
   con `chronon3d_cli validate --plan`.

## Validazione Chronon (2026-11-21)

```
CHRONON_BIN=<...>/chronon3d_cli go test ./internal/overlay/ -run ChrononValidation -count=1
```

- `entity-card`: `status PASS`, `valid true`, 2 layer, plan v3 (1600×900 → card 480×270, caption pinnata).
- `camera-pan-chain`: `status PASS`, `valid true`, 6 layer, plan v3, 1280×720.
- `composite-group`: `status PASS`, `valid true`, 4 layer, plan v3, 1920×1080.
- `camera-sequence`: `status PASS`, `valid true`, 4 layer, plan v3, 1920×1080.
- Il CLI esce 0 anche quando rifiuta: l'asserzione è il campo JSON `valid`.
- Il test crea le fixture asset/font nella temp root e copia i font reali del
  modulo, così la preflight del validator risolve ogni path.

## Verifica eseguita

```
go test ./internal/overlay/ -run SizedCompile -count=1
go test ./internal/overlay/ ./internal/processor/ -count=1
go test ./internal/chronon/ ./internal/workspace/ ./internal/renderbatch/ -count=1
go test ./... -count=1
```

## Rischio residuo

Il doppio lowering raddoppia il costo CPU del compile (non l'I/O). Se diventa
misurabile, il ticket successivo può far decodificare al pass A i soli asset ref
senza lowering completo.
