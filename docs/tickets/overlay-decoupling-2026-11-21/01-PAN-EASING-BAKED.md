# OVL-01 — Easing camera-pan di gruppo cotto nelle keyframe

**Stato:** PASS · **Priorità:** P0

**Esito:** `cameraPanTrack` ora cuoce la curva per-frame e dichiara `linear`,
identico alla chain. Aggiornati il test di direzione (boundary conservati,
densità per-frame), il test di parità (key identiche chain/group) e aggiunto
`TestCameraPanPlansUseOneRendererComposableTrackShape`, che vieta easing non
lineare sui component track di un piano compilato. Verifica eseguita:
`go test ./internal/overlay/ -run 'CameraPan|PanStrip|ChainPan'` e suite overlay
completa, verdi.

**Follow-up 2026-11-21 (OVL-07):** rimosso anche il residuo `bakeEasing=false`
con il suo commento "group sparso": `panStrip.track` ora cuoce sempre la curva
e dichiara sempre `linear`, senza un secondo ramo costruibile. Vedi
[OVL-07](07-CAMERA-CLEANUP-FOLLOWUP.md).

## Evidenza

`Chronon3d/src/render_plan/render_plan_compiler_animation.cpp`:

- ramo `pos_tracks.x || pos_tracks.y || pos_tracks.z` (component track),
- `add_component_vector_track` sceglie come `reference` la prima track con
  easing non lineare (`track->easing != kLinear`), poi pretende che **tutte** le
  component track abbiano `keyframes.size()`, `easing`, `interpolation` e timing
  identici, altrimenti:

  `component tracks with per-key timing must share keyframes and track easing`.

Oggi `image_group_camera_pan.go` emette `position_x` con 6 key sparse e
`Easing: "in_out_sine"`, mentre `position_y` è neutra (2 key, easing lineare).
Quella coppia viola il contratto del renderer. La chain camera-pan, invece,
cuoce già l'easing per-frame e dichiara `linear` su entrambe le axis: è la forma
che il renderer sa comporre (fallback `dominant_easing` → lineare).

## Problema

La stessa coreografia ha due rappresentazioni: una compatibile col renderer
(chain, cotta) e una che il renderer rifiuta o interpreta in modo diverso
(group, dichiarata). Non è solo drift estetico: è un piano non eseguibile.

## Target

Una sola rappresentazione: entrambi i consumer usano `panStrip` con
`bakeEasing=true` ed `Easing: "linear"`. La differenza rimasta tra chain e group
è solo il track `scale` del recipe (1.02→1.06), che è presentazione del group e
non altera la scaletta.

## Piano

1. `cameraPanTrack` chiama `strip.track(property, "linear", true, …)`.
2. Aggiornare `TestCameraPanDirectionsHoldThenTravelToTheNextImage`: la scaletta
   resta a 6 boundary (0, 13, 32, 45, 64, 95) ma le key diventano per-frame,
   easing `linear`.
3. `pan_strip_test.go`: sostituire l'assunzione "group sparso / chain cotto" con
   un test che verifica la forma renderer: tutte le component track lineari, x e
   y con gli stessi frame di ownership? (La y neutra resta a 2 key: il renderer
   la accetta solo perché nessuna track è `reference`; il test deve pinnare
   esattamente questo.)
4. Verificare che il test single-image (settle a frame 59) continui a passare.

## Criteri di accettazione

1. `cameraPanTrack` produce `Easing=="linear"` e una key per ogni frame di
   viaggio; nessuna key con easing dichiarato.
2. Il test di parità dimostra che chain e group espongono gli stessi valori ai
   boundary e la stessa densità per-frame.
3. Nessuna component track del piano ha easing non lineare quando l'altra axis
   ha keyframe diverse (invariante "renderer-composable").
4. Suite overlay e processor verde.

## Verifica

```
go test ./internal/overlay/ -run 'CameraPan|PanStrip|ChainPan' -count=1
go test ./internal/overlay/ ./internal/processor/ -count=1
```

## Rischio

Il gruppo perde l'easing dichiarato nel JSON: chi ispeziona il piano vede la
curva cotta. È lo stesso contratto già accettato per la chain ed è l'unico che
il renderer esegue.
