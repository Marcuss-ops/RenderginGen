# Overlay decoupling — ticket del 2026-11-21

**Data:** 2026-11-21
**Origine:** analisi di `internal/overlay` + `internal/processor` (mappa dei 5
percorsi multi-immagine, churn degli ultimi 60 commit). La wave precedente ha
chiuso il primo blocco: motore strip unico, chain divisa per choreography,
`Plan.Features`, entity group fuori dal legacy, `finalizeImageLayers`, geometria
del fit spostata in overlay. Questa wave affronta i blocchi rimasti più difficili
e i due gate repo-wide che impediscono `go test ./...` verde.

## Stato complessivo

| ID | Area | Stato | Priorità |
| --- | --- | --- | --- |
| [OVL-01](01-PAN-EASING-BAKED.md) | Easing camera-pan di gruppo: una sola rappresentazione compatibile col renderer | PASS | P0 |
| [OVL-02](02-CHAIN-SEGMENTS-TYPED.md) | `chain_segments` tipizzato sul contratto interno (niente round-trip JSON) | PASS | P1 |
| [OVL-03](03-ASSET-SIZES-BEFORE-COMPILE.md) | Dimensioni asset prima del compile: zero mutazioni post-compile | PASS | P0 |
| [OVL-04](04-SEQUENCE-CAMERA-OFF-LEGACY.md) | Camera sequence fuori da `compileLegacyCompositeImageLayers` | PASS | P2 |
| [OVL-05](05-BACKDROP-SINGLE-ENTRYPOINT.md) | Un solo entry point per `image_backdrop` | PASS | P2 |
| [OVL-06](06-REPO-GREEN-GATES.md) | Gate repo-wide: architettura + dead exports | PASS | P1 |
| [OVL-07](07-CAMERA-CLEANUP-FOLLOWUP.md) | Debito camera: conflitti sequence fail-closed, strip unico, baker esplicito, freeze `scene_camera` | PASS | P0/P1 |

## Verifica finale indipendente (2026-11-21, riverificata dopo l'audit)

Eseguita dopo l'ultima modifica della wave, non solo dopo la chiusura dei
singoli ticket:

- `go build ./...` → OK
- `go vet ./internal/overlay ./internal/processor` → OK
- `go test ./internal/overlay/ ./internal/processor/ -count=1` → ok (2,6 s / 38,2 s)
- `go test ./internal/chronon/ ./internal/workspace/ ./internal/renderbatch/ -count=1` → ok
- `go test ./internal/architecture/ -count=1` → ok (41,2 s)
- `go test ./... -count=1` → exit 0 (40 package con test, 0 FAIL). Il rerun
  dopo l'aggiunta del test di validazione ha però riprodotto un fallimento in
  `internal/processor` (`TestCanonicalizeAssemblyV1EmitsPinnedStreamAndKeepsAudio`,
  `closed GOP uncertifiable`): non era un flake, era la race
  `StdoutPipe`+`Wait` di `internal/media/gop_probe.go`, corretta e verificata
  con `-count=10` nel ticket
  [`TICKET-GOP-PROBE-WAIT-RACE-2026-11-21`](../../tickets/TICKET-GOP-PROBE-WAIT-RACE-2026-11-21.md).
  La suite completa è verde dopo il fix.
- Validazione sul motore reale: `TestCompiledMultiImagePlansPassChrononValidation`
  (opt-in via `CHRONON_BIN`) invoca
  `chronon3d_cli validate --plan <plan> --assets-root <root>` sui piani compilati
  entity card (2 layer), camera-pan chain (6), composite group (4) e camera
  sequence (4): tutti `status: PASS`, `valid: true`, plan v3. Il test asserisce
  il campo JSON `valid`, non l'exit code: il CLI esce 0 anche su piano
  rifiutato.
- Parità sized/post-fit estesa a **tutte** le proiezioni multi-immagine:
  `TestSizedCompileMatchesThePostCompileFit` (entity card),
  `TestSizedCompileMatchesThePostFitOnACameraPanChain`,
  `TestSizedCompileMatchesThePostFitOnACompositeGroup` (legacy replay, due
  member e aspect diversi),
  `TestSizedCompileMatchesThePostFitOnACameraSequence` (viewport e caption
  screen-space): confronto JSON dell'intera lista layer, verde.
- Audit camera successivo (OVL-07): i conflitti della sequence camera sono
  fail-closed, il ramo `bakeEasing=false` dello strip è rimosso e il camera
  baker è passato via `CompileOptions` invece che da una globale mutabile.
  `scene_camera` è congelato: contratto mantenuto, nessun producer attivo.
- Grep strutturali: `decodeChainSegments` solo in normalizzazione
  (`semantic_compile.go:240`); corpo di `compileCompositeImageMembers` senza
  `CameraMotionID`/`sequenceTravelMS`/`Enable3D`/`ScreenSpace`;
  `sequence_camera_compile.go` presente; `addImageBackdrops` è l'unica funzione
  che appende backdrop (`addSingleImageBackdrop`, `addCompositeImageBackdrops`
  e `addBackdropGrid` non esistono più).

## Regole di questa wave

- Ogni ticket dichiara un criterio di accettazione verificabile con un comando.
- Un ticket si chiude solo con la verifica eseguita **dopo** l'ultima modifica.
- Nessun cambio di contratto verso PipelineGen: i campi wire restano quelli
  pubblicati; cambia solo la rappresentazione interna.
- Un comportamento del renderer non si presume: si cita la riga che lo impone.
