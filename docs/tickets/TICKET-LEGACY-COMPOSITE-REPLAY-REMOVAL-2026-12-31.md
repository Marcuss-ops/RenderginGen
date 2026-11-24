# TICKET — Ritiro del replay composite legacy

**Stato:** CLOSED — rimosso il 2026-11-24, in anticipo sulla deadline
**Priorità:** P2
**Owner:** RenderingGen overlay
**Deadline:** 2026-12-31
**Origine:** OVL-04/OVL-08; census legacy composite del 2026-11-18 e del 2026-11-24.

## Perché rimuoverlo

`compileLegacyCompositeImageLayers` era l'unico path di replay per i piani
sealed che precedono `group_motion_id`. Era ammesso solo con
`legacy_composite_replay=true`; un piano nuovo con `image_layers` e senza
`group_motion_id` falliva chiuso.

Il census del 2026-11-18 su RenderingGen, PipelineGen e BrainRust ha trovato
**zero** piani in-repo con `image_layers` non vuoto. La finestra dichiarata
sarebbe scaduta il **2026-12-31**. Tenerla oltre la scadenza avrebbe significato
mantenere due semantiche di motion per-child e un marker wire che nessun
producer nuovo usa; per questo la rimozione è stata anticipata.

## Programma

| Data | Azione |
|---|---|
| 2026-11-24 | Rimozione anticipata eseguita: codice, contratto, fixture, test, metriche e documentazione. Il census in-repo resta a zero piani production; nessuna proroga. |
| 2026-12-01 | Census finale esterno, ora solo di verifica: se un artefatto sealed fuori dal checkout richiede ancora per-child motion, il render fallisce con un errore esplicito e va aperto un ticket dedicato con owner e data. |
| 2026-12-31 | Deadline storica. Il marker non è più accettato dal decoder. |

La rimozione ha eliminato anche il gate `TestLegacyCompositeReplayWindowExpires`
(`renderinggen/internal/overlay/legacy_composite_replay_test.go`) e la costante
`legacyCompositeReplayDeadline`, che non hanno più un soggetto. Il controllo
negativo ora è `TestCompositeReplayMarkerIsRejected`: il decoder rifiuta il
campo sconosciuto e `TestCompositeWithoutGroupMotionFailsClosed` pinna il
errore esplicito per un composite nuovo senza coreografia.

## Censimento

```sh
# piani con image_layers nel repository
grep -rn '"image_layers"' --include='*.json' --include='*.go' . | grep -v _test

# marker e simboli del replay
grep -rn 'legacy_composite_replay\|compileLegacyCompositeImageLayers\|LegacyComposite' \
  --include='*.go' --include='*.json' --include='*.md' . | grep -v _test
```

Il census del 2026-11-18 è la baseline; va ripetuto il 2026-12-01 sugli
artefatti sealed esterni, che non sono in questo checkout.

**Aggiornamento 2026-11-24 (RenderingGen):** il census di questo checkout trova
**0 piani di produzione** con `image_layers` non vuoto. Le cinque occorrenze di
`"image_layers"` sono schema (`contracts/overlay-plan.v1.schema.json`), tipi del
contratto e due comandi demo, non piani sealed. Baseline esterna invariata:
nessuna proroga è stata concessa. Vedi
`docs/operations/legacy-alias-census.md` per owner e date delle tabelle di alias
non-composite.

## Cosa è stato rimosso

### Codice di produzione

- `compileLegacyCompositeImageLayers` in
  `renderinggen/internal/overlay/semantic_image_compile.go` e il dispatch che
  la chiamava in `compileMultiImageItem`;
- il campo `LegacyCompositeReplay` del piano
  (`renderinggen/internal/overlay/semantic_contract_types.go`) e i flag
  `LegacyCompositeImage`/`LegacyCompositeReplay` in
  `renderinggen/internal/overlay/semantic_compile.go`;
- i contatori `LegacyCompositeImageCount`/`LegacyCompositeReplayCount` in
  `renderinggen/internal/overlay/stats.go`;
- le metriche `LegacyCompositeImageItems`/`LegacyCompositeImageReplay` in
  `renderinggen/internal/metricnames/metricnames.go` e il loro uso in
  `renderinggen/internal/processor/prepare_stages.go`;
- il campo `legacy_composite_replay` da
  `contracts/overlay-plan.v1.schema.json`;
- la pagina `docs/operations/legacy-composite-replay-window.md`, eliminata.

### Test e fixture

- `renderinggen/internal/overlay/legacy_composite_replay_test.go` (contatori
  `1/1`) è stato rimosso con il path che verificava;
- `TestLegacyCompositeStillCompilesPerChildMotion`
  (`group_motion_compile_test.go`) è stato rimosso;
- `TestSizedCompileMatchesThePostFitOnACompositeGroup` (`sized_compile_test.go`)
  è stato **convertito** al path `group_motion_id`, preservando il confronto
  sized/post-fit: è l'unica copertura di quella combinazione;
- i fixture che impostavano `legacy_composite_replay: true` solo per admission
  sono stati ripuliti: i casi moderni dichiarano `group_motion_id`, mentre i
  test espliciti del replay sono spariti con il path. Il censimento copriva
  `image_effects_runtime_test.go`, `semantic_sfx_test.go`,
  `image_clip_parity_test.go`, `selection_model_test.go`,
  `image_group_camera_pan_test.go`, `image_premium_v1_test.go`,
  `entity_payload_font_precedence_test.go`, `composite_image_runtime_test.go`,
  `chronon_plan_validation_test.go`, `composite_three_image_runtime_test.go`,
  `semantic_image_grid_dedupe_test.go`, `image_backdrop_members_test.go`,
  `image_effects_paths_test.go`, `overlay_v3_catalog_test.go` e
  `group_motion_compile_test.go`.

La rimozione dei fixture è stata completata nello stesso cambio; i casi
moderni non dichiarano più motion per-child.

## Esito della rimozione (2026-11-24)

- Il path `compileLegacyCompositeImageLayers`, il marker
  `legacy_composite_replay`, i due contatori `Stats`, le due metriche del
  processor e la proprietà schema sono stati rimossi.
- I fixture moderni dichiarano ora una coreografia `group_motion_id`
  (`image_group_slide_right` nei test riutilizzabili 2–5 membri); i test che
  verificavano il per-child motion sono stati rimossi o convertiti.
- Un child che dichiara `motion_id`/`motion_params` mentre il parent ha
  `group_motion_id` viene rifiutato con un errore esplicito, invece di essere
  ignorato in silenzio: il group recipe è l'unico owner della coreografia.
- La pagina `docs/operations/legacy-composite-replay-window.md` è stata
  eliminata; la finestra è chiusa, non prorogata.
- Il group compiler continua a emettere la griglia del composite una sola
  volta, con la precedenza del blocco item-level su quello child-level.

## Criteri di accettazione

- [x] `grep -rn 'legacy_composite_replay\|compileLegacyCompositeImageLayers\|LegacyComposite'`
      non trova più riferimenti di produzione; restano i test negativi che
      verificano il rifiuto.
- [x] Un piano con `legacy_composite_replay=true` viene rifiutato con un errore
      esplicito (`unknown field "legacy_composite_replay"`), non ignorato in
      silenzio.
- [x] Un child di un composite con `group_motion_id` che dichiara
      `motion_id`/`motion_params` viene rifiutato con un errore esplicito.
- [x] Schema e contract test aggiornati; i tre mirror restano coerenti.
- [x] `go build ./...`, `go vet`, `go test ./internal/overlay/ ./internal/processor/`,
      `make test-unit` e `make deadcode` verdi.
- [x] `docs/operations` aggiornato: la finestra è chiusa, non prorogata.
- [x] Nessun golden rigenerato: la rimozione non cambia i piani dei fixture
      convertiti oltre alla dichiarazione della coreografia di gruppo.

## Rischi residui

- **Artefatto sealed esterno non censito.** Il render fallisce con
  `unknown field "legacy_composite_replay"`; il census del 2026-12-01 resta la
  verifica esterna. Non si ripristina il path “per sicurezza”: sarebbe una
  proroga di fatto.
- **Fixture diffusi.** Il marker era usato in molti test come scorciatoia di
  admission: i casi moderni sono stati convertiti a `group_motion_id` e il
  test negativo del marker resta a pinnare il rifiuto.
- **Copertura sized/post-fit.** `TestSizedCompileMatchesThePostFitOnACompositeGroup`
  è stato convertito, non eliminato: la parità sized/post-fit del composite
  resta pinnata.
