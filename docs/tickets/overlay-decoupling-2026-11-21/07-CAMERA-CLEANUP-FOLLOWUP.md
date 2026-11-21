# OVL-07 — Chiusura del debito camera emerso dall'audit

**Stato:** PASS · **Priorità:** P0/P1 · **Data:** 2026-11-21

**Origine:** audit indipendente dei cinque meccanismi camera di
`internal/overlay`. I ticket OVL-01…06 erano verdi, ma l'audit ha mostrato che
tre difetti erano ancora nel codice: conflitti della sequence camera senza
errore, un ramo `bakeEasing=false` residuo nello strip condiviso e un baker
camera installato come globale mutabile. Questo ticket chiude quei tre punti e
registra la decisione su `scene_camera`.

## 1. Sequence camera: conflitti fail-closed (P0)

> **Aggiornamento OVL-08:** questo percorso è stato ritirato end-to-end. I
> conflitti fail-closed restano validi per le tre authority rimaste
> (`scene_camera`, map, entity); `TestSequenceCameraRejectsEveryCompetingController`
> è stato rimosso insieme ai file sequence.

### Evidenza

`semantic_camera_controllers.observe` accettava il primo `CameraMotionID` e
ignorava i successivi. `validate` non considerava la sequence camera, quindi un
piano con sequence + map camera o sequence + entity-style camera passava:
`compileSequenceCameraImages` aveva già parcheggiato i child a
`x = index*canvasWidth` (`sequence_camera_compile.go`) e `apply` saltava la
sequence. Le foto oltre la prima restavano fuori schermo, senza errore.

### Esito

- `observe` rifiuta un secondo controller sequence, sia esso un item con
  `camera_motion_id` sia una chain con `chain_camera` camera-sequence.
- `validate` trattava le quattro authority come mutuamente esclusive:
  `scene_camera`, map camera, entity-style camera e sequence camera. La
  sequence non era più un fallback silenzioso. (Storico: dopo OVL-08 restano
  tre authority e il percorso sequence non esiste più.)
- Il piano non viene sigillato: `lowerPlan` chiama `apply` prima di
  `sealPlan`, quindi nessun layout con foto parcheggiate può uscire dal
  compiler.
- Test: `TestSequenceCameraRejectsEveryCompetingController` copre due
  sequence, sequence+map in entrambi gli ordini, sequence+entity in entrambi
  gli ordini e scene_camera+sequence.

### Criteri di accettazione

1. **PASS** — due `camera_motion_id` nello stesso piano falliscono con
   `only one scene camera controller is allowed per plan`.
2. **PASS** — sequence + map camera / entity-style camera fallisce in
   qualunque ordine degli item.
3. **PASS** — scene_camera + sequence fallisce.
4. **PASS** — nessun test di sequence esistente cambia aspettativa.

## 2. Pan strip: una sola rappresentazione (P0)

### Evidenza

`pan_strip.go` esponeva ancora `track(property, easing, bakeEasing, spacing,
travelSign)` con un ramo non cotto. Entrambi i consumer passavano già
`"linear", true`; il ramo `false` era morto, ma il commento descriveva ancora
il group come “sparse staircase + declared easing”, cioè la forma che Chronon
rifiuta.

### Esito

- `panStrip.track(property, spacing, travelSign)` cuoce sempre la sine per
  frame e dichiara sempre `linear`. Il flag e l'easing argomento non esistono
  più: non c'è un secondo modo di costruire la track.
- Aggiornati `chainPanTrack`, `cameraPanTrack`, `pan_strip_test.go` e i
  commenti di testata.
- `TestPanStripIsTheSingleStaircase`, i test camera-pan del group e i test
  della chain restano invariati e verdi.

### Criteri di accettazione

1. **PASS** — `grep -rn bakeEasing internal/overlay` è vuoto.
2. **PASS** — chain e group producono la stessa track cotta/lineare per gli
   stessi arrivi (`TestPanStripIsTheSingleStaircase`).
3. **PASS** — nessuna component track del piano dichiara easing non lineare
   (`TestCameraPanPlansUseOneRendererComposableTrackShape`).

## 3. Camera baker: dipendenza esplicita, niente globale (P1)

### Evidenza

`camera_samples.go` installava il baker in `var cameraSampleBaker` tramite
`SetCameraSampleBaker`, chiamato dal composition root. Lo stato era mutabile e
process-wide, non passato al compile; due compile concorrenti non potevano
avere authority diverse.

### Esito

- Nuovo `overlay.CompileOptions{AssetSizes, CameraBaker, SkipCameraBake}` e
  nuovo entry point `CompileSemanticWithOptions`. `CompileSemantic` e
  `CompileSemanticWithAssetSizes` sono wrapper che passano opzioni vuote/sized.
- `SkipCameraBake` è usato dal pass A di `PrepareJob`/`materialize.Prepare`:
  il piano transitorio conserva le track ma non invoca il baker, quindi un
  piano animato non viene cotto due volte. Il pass B resta l'unico a produrre
  `camera_samples`.
- `sealPlan` riceve il baker per parametro; `bakeResolvedCameraSamples(plan,
  baker)` non legge più stato di package.
- Baker `nil` seleziona `referenceCameraBaker`, l'evaluator Go deterministico
  ora in produzione (prima viveva solo nei test). Il worker inietta il runner
  nativo CM3D via `processor.Options.CameraBaker`; `cmd/renderinggen` lo
  costruisce con `camerabake.FromEnv()`.
- `materialize.go` passa dal seam del processor, quindi anche il path
  prepare-only usa il baker iniettato.
- Test: `TestCompileOptionsUseTheInjectedCameraBaker` prova che il compile usa
  il port iniettato; i test exec/timeout usano `camerabake.New` senza toccare
  stato globale.

### Criteri di accettazione

1. **PASS** — `SetCameraSampleBaker`, `var cameraSampleBaker` e ogni setter
   process-wide sono rimossi.
2. **PASS** — il baker arriva a `sealPlan` solo attraverso `CompileOptions`.
3. **PASS** — il worker nativo è ancora iniettato dal composition root; un
   binario non configurato fallisce con l'errore nominato già esistente.
4. **PASS** — il fallback in-process è deterministico e coperto da test.

## 4. `scene_camera`: decisione di freeze (P2)

### Evidenza

La ricerca dei producer attivi non trova alcun piano o codice che emetta
`scene_camera`: l'unica occorrenza in `refactored/` è un commento in
`map_composition.go`; il contratto e i test sono in RenderingGen. Il blocco è
però un contratto approvato (`camera_roll_contract_test.go`, tre movimenti
`scene_dolly_through` / `scene_lateral_handoff` / `scene_crane_reveal`) e
Chronon sa eseguirlo via `camera_samples`.

### Decisione

**Freeze, non rimozione.** Il blocco resta nel contratto e nel lowering, ma non
è promosso né esteso finché non esiste un producer attivo. Rimuoverlo ora
sarebbe una decisione di prodotto e romperebbe un contratto pubblicato senza un
sostituto verificato.

La sequence camera, invece, è stata **ritirata** in
[OVL-08](08-SEQUENCE-CAMERA-RETIRED.md): nessun producer la emetteva, lo schema
esponeva un campo non costruibile e i caroselli reali usano `group_motion_id`
con la famiglia camera-pan. La duplicazione concettuale del punto 1 non esiste
più perché il percorso è stato rimosso, non solo reso fail-closed.

### Criteri di accettazione

1. **PASS** — nessun producer attivo di `scene_camera` nel repository.
2. **PASS** — i test del contratto scene camera restano verdi e non vengono
   indeboliti.
3. **PASS** — la decisione di freeze è registrata in questo ticket e
   nell'indice della wave.
4. **PASS** — la sequence camera è ritirata in OVL-08; il freeze resta limitato
   a `scene_camera`.

## Verifica

Eseguita dopo l'ultima modifica della wave:

```
go build ./...                                            → OK
go vet ./internal/overlay ./internal/processor            → OK
go test ./internal/overlay/ ./internal/processor/ -count=1 → ok (2,608 s / 38,911 s)
go test ./internal/architecture/ -count=1                 → ok (38,7 s)
go test ./... -count=1                                    → exit 0 (verificato con pipefail)
```

Test mirati:

```
go test ./internal/overlay/ -run 'SceneCameraRejectsMultipleControllers' -count=1
go test ./internal/overlay/ -run 'CameraPan|PanStrip|ChainPan' -count=1
go test ./internal/overlay/ -run 'BakeResolvedCameraSamples|CompileOptionsUseTheInjectedCameraBaker|CompileOptionsSkipCameraBakeDefersTheRenderPass|CameraBaker' -count=1
go test ./internal/processor/ -run 'CompileSemanticForPrepare' -count=1
```

## Rischio residuo

- **Risolto:** il pass A di `PrepareJob` non cuoce più la camera; usa
  `SkipCameraBake` e lascia il bake al pass B. La copertura è
  `TestCompileOptionsSkipCameraBakeDefersTheRenderPass` e
  `TestCompileSemanticForPrepareUsesTheOptionalSeam`.
- **Risolto (OVL-08):** la sequence camera non è più un secondo meccanismo;
  campo wire, lowering, test e golden sono stati rimossi.
- `scene_camera` resta codice dormiente: va rimosso solo insieme a un
  sostituto di prodotto, non per igiene isolata.
