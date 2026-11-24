# OVL-04 — Camera sequence fuori da `compileLegacyCompositeImageLayers`

> **SUPERSEDED da [OVL-08](08-SEQUENCE-CAMERA-RETIRED.md) (2026-11-21).** La
> sequence camera è stata ritirata end-to-end: questo ticket resta come
> cronologia dell'estrazione, ma i file, il golden e i test citati non
> esistono più nell'albero corrente.

**Stato:** PASS (storico) · **Priorità:** P2

## Evidenza iniziale

`compileCompositeImageMembers` conteneva due blocchi di una sola feature:

1. `ri.Item.CameraMotionID != ""` riscriveva box, `Fit=cover`, radius, mask,
   shadow/glow, `Enable3D`, azzerava animazioni e disponeva i member a
   `index*canvasWidth`;
2. il ramo caption della sequence calcolava `sequenceTravelMS`, convertiva la
   caption in coordinate assolute e la staccava dall'immagine.

## Esito

- Nuovo file `internal/overlay/sequence_camera_compile.go`:
  - `isSequenceCameraItem`;
  - `compileSequenceCameraImages(ri, src, registry)`: lowering completo dei
    child (un solo layer, nessun premium, nessuna grid), viewport,
    `applySequenceCameraRig`, effetti child, caption screen-space;
  - `sequenceImagePosition`, `applySequenceImageViewport`,
    `applySequenceCameraRig`, `layoutSequenceCaption`.
- `compileMultiImageItem` dispatcha a `compileSequenceCameraImages` **prima** di
  qualunque recipe (group/premium/legacy).
- `compileCompositeImageMembers` non nomina più `CameraMotionID`, `Enable3D`,
  `ScreenSpace` o `sequenceTravelMS`; il ramo caption resta solo quello
  composito (`caption_start_ms`/`caption_end_ms`).
- `imageChildOptions.sequence` resta, ma lo imposta solo il compiler sequence.

## Criteri di accettazione

1. **PASS** — grep del primitivo composito:
   `grep -n "CameraMotionID\|sequenceTravelMS\|Enable3D\|ScreenSpace"
   internal/overlay/semantic_image_compile.go` restituisce solo la riga di
   `compileSingleImageLayerResolved` (funzione condivisa, non
   `compileCompositeImageMembers`).
2. **PASS** — `TestSequenceCameraLoweringMatchesSnapshot` confronta l'intera
   lista layer (ordine, ID, posizioni, finestre, caption assoluta) con
   `internal/overlay/testdata/sequence_camera_layers.golden.json`, registrato
   **prima** dell'estrazione: nessun campo è cambiato.
3. **PASS** — test `Composite|Sequence|Group|Camera` verdi, incluso il composite
   a tre immagini e `TestSizedCompileMatchesThePostFitOnACameraSequence`, che
   prova che il sized compile non introduce un fit per-source nella sequence.
4. **PASS (storico)** — all'epoca `compileLegacyCompositeImageLayers` era
   raggiungibile solo con `legacy_composite_replay` oppure dal dispatcher
   sequence, che non lo attraversava. Il path e il marker sono stati rimossi il
   2026-11-24 con `TICKET-LEGACY-COMPOSITE-REPLAY-REMOVAL-2026-12-31`.

## Verifica eseguita

```
go test ./internal/overlay/ -run 'SequenceCameraLoweringMatchesSnapshot|Composite|Sequence|Group|Camera' -count=1
go test ./internal/overlay/ ./internal/processor/ -count=1
CHRONON_BIN=<...>/chronon3d_cli go test ./internal/overlay/ -run ChrononValidation -count=1
```

La sequence compilata passa il validator reale di Chronon (`status PASS`,
`valid true`, 4 layer, plan v3), insieme alla camera-pan chain: vedi ticket 03
§Validazione Chronon.

## Rischio residuo

La geometria della caption assoluta è delicata; lo snapshot è il guardiano.
Qualunque modifica futura al layout sequence deve aggiornare il golden con una
motivazione esplicita, mai rigenerarlo per far passare il test.
