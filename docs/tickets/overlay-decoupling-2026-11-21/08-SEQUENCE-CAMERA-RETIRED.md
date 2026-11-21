# OVL-08 — Ritiro completo della sequence camera

**Stato:** PASS · **Priorità:** P1 · **Data:** 2026-11-21
**Sostituisce:** OVL-04 (la cui estrazione resta come cronologia) e la parte
sequence di OVL-07 §4.

## Origine

OVL-07 aveva congelato la sequence camera invece di rimuoverla, in attesa di un
producer attivo. L'audit successivo ha verificato che quel producer non esiste:

- nessun codice in `BrainRust/`, `refactored/` o `RenderingGen/` assegna
  `camera_motion_id` a un item o a un chain segment;
- `entity_sequence_camera` compare solo nel lowering e nei test di
  RenderingGen, non in un piano o in una risposta del producer;
- l'unico consumatore reale di caroselli fotografici è `group_motion_id` con la
  famiglia camera-pan (`image_group_camera_pan_*`), già verificata dal motore;
- lo schema pubblico esponeva ancora `camera_motion_id`, quindi un piano
  poteva richiedere un meccanismo che nessun producer sapeva costruire.

Tenere il campo significava mantenere due meccanismi camera concorrenti, un
golden e cinque test per un percorso irraggiungibile. La decisione è quindi il
ritiro end-to-end, non un secondo freeze.

## Modifiche

### RenderingGen (consumatore)

- Rimosso `semanticItem.CameraMotionID` e `chainSegment.CameraMotionID`.
- Rimosso `chainSequenceCamera` e il relativo dispatch in `chain_compile.go`.
- Rimosso il dispatch `compileSequenceCameraImages` da
  `compileMultiImageItem`, il ramo `sequenceStatic` e ogni lettura di
  `CameraMotionID` nella compilazione immagini.
- Rimossi `imageChildOptions.sequence`, `imageAssetIsLandscape` e
  `sequenceImageLayoutParams`: il child resolver non apre più il file per
  dedurre l'orientamento, perché non esiste più un viewport sequence da
  costruire.
- Eliminati `sequence_camera_compile.go`, `entity_sequence_camera.go`,
  `sequence_camera_snapshot_test.go`, `entity_sequence_camera_test.go`,
  `semantic_camera_sequence_conflict_test.go`,
  `testdata/sequence_camera_layers.golden.json` e il caso sequence di
  `TestSizedCompileMatchesThePostFitOnACameraSequence`.
- `cameraControllerSet` gestisce solo le tre authority rimaste:
  `scene_camera`, map camera e entity-style camera.
- `decodeChainSegments` ora usa `DisallowUnknownFields`: un
  `camera_motion_id` annidato in un segment non viene ignorato, ma fallisce
  con lo stesso errore esplicito del campo item.

### Contratto

- Rimosso `camera_motion_id` da
  `RenderingGen/contracts/overlay-plan.v1.schema.json`.
- Il decoder rifiuta il campo come unknown field; il nuovo
  `TestRetiredSequenceCameraFieldIsRejected` pinna l'errore e il nome del
  campo, così il ritiro non può regredire in un'ignoranza silenziosa.
- Il contratto `chain_segments` passa da 27 a 26 chiavi nei tre mirror:
  Rust (`CANONICAL_KEYS`), PipelineGen (`canonicalChainSegmentKeys`) e
  RenderingGen (`canonicalChainSegmentKeys`).

### Producer e PipelineGen

- Rimossi `camera_motion_id` da `PlanItemV2` e dal segment coalescato in
  `BrainRust/overlay-compiler`.
- Rimossi `CameraMotionID` da `capmodel.PlanItem`/`ChainSegment`, dalla
  validazione animation, dal binding `overlay_plan_binding.go` e dal contesto
  cache `overlay_render_cache.go`.
- Rimossi i valori `camera_motion_id` dai fixture di test che non li
  asserivano più.

## Cosa resta

- `scene_camera`: invariato e congelato (OVL-07 §4), nessun producer attivo.
- Camera-pan di gruppo e chain: resta il meccanismo unico `pan_strip`, cotto
  per frame e dichiarato `linear`.
- Entity-style camera: resta l'unico `CameraMotionID` interno, ma è un campo
  di `entityStyle`, non un campo wire dell'item.
- `chain_camera` (direzione del pan di una chain) non è toccato: non è la
  sequence camera.

## Criteri di accettazione

1. **PASS** — `grep -rn camera_motion_id` nei sorgenti e nello schema non
   trova più un campo wire; resta solo la stringa nel test di ritiro.
2. **PASS** — un piano con `camera_motion_id` fallisce al decode con un errore
   che nomina il campo, sia sull'item sia dentro un chain segment
   (`TestRetiredSequenceCameraFieldIsRejected`,
   `TestRetiredSequenceCameraFieldIsRejectedInChainSegments`).
3. **PASS** — `sequence_camera_compile.go`, `entity_sequence_camera.go` e i
   test/golden sequence non esistono più.
4. **PASS** — i conflitti scene/map/entity restano fail-closed
   (`TestSceneCameraRejectsMultipleControllers` copre map+entity e i duplicati
   di ciascuna authority).
5. **PASS** — i tre mirror di `chain_segments` dichiarano 26 chiavi e i test
   Rust/PipelineGen/RenderingGen sono verdi.
6. **PASS** — `go build ./...`, `go vet`, suite RenderingGen completa e suite
   contract PipelineGen sono verdi.

## Verifica eseguita

```text
gofmt -l internal/overlay internal/processor        → vuoto
go build ./...                                      → OK
go vet ./internal/overlay ./internal/processor      → exit 0
go test ./internal/overlay/ ./internal/processor/   → ok (2,608 s / 38,911 s)
go test ./internal/overlay/ -run 'RetiredSequenceCamera' -count=1 → ok
go test ./... -count=1                              → exit 0
refactored: go build ./...                          → exit 0
refactored: go test ./internal/capabilities/overlays/...
            ./internal/capabilities/scripts/...     → ok
BrainRust/overlay-compiler: cargo fmt -- --check    → exit 0
BrainRust/overlay-compiler: cargo test --lib segment_contract_tests
                                                    → 2 passed, 0 failed
```

La validazione Chronon opt-in non è stata rieseguita in questa sessione perché
`CHRONON_BIN` non è configurato; il caso sequence è stato rimosso dal test di
validazione insieme al percorso che lo produceva.

## Rischio residuo

- Un producer esterno non presente in questo repository che emettesse ancora
  `camera_motion_id` riceverebbe un errore di decode esplicito. È il
  comportamento voluto: il campo non ha più un lowering.
- `scene_camera` resta codice dormiente e va rimosso solo con un sostituto di
  prodotto, non per igiene isolata.
