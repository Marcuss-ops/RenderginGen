# Changelog — 22 ottobre 2026 — RenderingGen

Un solo commit oggi su `main`: `7bbb0d4` — *Unify overlay SSOT vocabularies and
camera construction, split workerlog/semantic compile* (113 file,
+5.552/−4.485). Sotto il dettaglio per area.

## 1. Vocabolari overlay a proprietario unico (SSOT)

- Le spelling di item-kind e motion-target non sono più riscritte ai consumer:
  `overlay/registry.go` possiede le costanti `Kind`, `motion_admission.go` il
  vocabolario dei target, `premium_image_geometry.go` i `PathKind`.
- Due nuove regole architetturali in `renderinggen/internal/architecture/`,
  documentate in `CONFORMANCE.md`:
  - `item_kind_literal_outside_registry` — vieta una spelling distintiva di
    item-kind scritta letteralmente fuori da `registry.go` (i test sono
    esenti; le spelling generiche restano coperte dalla convenzione sulle
    costanti);
  - `motion_target_literal_outside_vocabulary` — lo stesso per i target
    distintivi (`map_view`, `short_phrase`) posseduti da `motion_admission.go`.
- Entrambe sono scoped ai sorgenti non-test di `renderinggen/internal/overlay` e
  vietano la stessa forma di errore: una decisione con spelling in un solo
  posto ripetuta a un consumer, dove le due viste possono divergere. La tabella
  in `CONFORMANCE.md` è una proiezione verificata di `Rules()`:
  `TestConformanceDocListsEveryRule` fallisce se i due set di rule-id
  differiscono.

## 2. Camera: un costruttore condiviso e pose risolte per frame

- Nuovo `overlay/camera_plan.go`: costruzione del `CameraPlan` wire e meccanica
  condivisa dei keyframe. I tre percorsi che emettono camera (rig di scena, map
  move, controller entity/sequence) mantengono la propria coreografia, ma
  ottiche (`design_tokens.go`), camera a riposo e regole di keyframe nascono qui.
  Prima la camera prospettica a riposo era due struct literal identici e il
  collasso dei keyframe sullo stesso frame era implementato due volte
  (`dedupeConsecutiveKeys` e `dedupeSequenceKeys`).
- Nuovo `overlay/camera_samples.go`: cuoce i track scalari temporanei del
  compiler in pose assolute per ogni frame di presentazione, perché il piano
  wire non ha track di proprietà indipendenti e C3D riceve una posa risolta per
  frame. Coperto da `camera_samples_test.go` (236 righe).
- Nuovo `overlay/semantic_camera_controllers.go`: raccoglie le authority di
  camera dichiarate dal piano e decide i conflitti una volta sola, dopo aver
  letto tutti gli item, così l'accettazione non dipende dall'ordine e l'errore è
  deterministico. Precedenza invariata:
  `scene_camera > map camera > entity-style camera > sequence camera`.

## 3. Lowering semantico ed effetti divisi per responsabilità

- `semantic_compile.go` passa da ~549 righe di diff a un dispatcher snello; il
  lavoro è distribuito in `semantic_entity_compile.go`,
  `semantic_video_compile.go`, `semantic_text_accents.go` (accenti
  frase/brush: componenti recipe emessi accanto al layer testo e ancora del
  blocco testo), `phrase_wrap.go` (politica di wrap condivisa da frasi,
  didascalie entità e misura layout) e `image_child_resolve.go` (campi che un
  child `image_layers` eredita dal parent, unico proprietario per composite,
  image-group e multi-image stack).
- Nuovo `semantic_effect_catalog.go`: catalogo unico degli effetti pubblici,
  con due superfici (`effectSurfaceLayer` per il params aperto di
  image/shape, `effectSurfaceClip` per lo stack tipizzato delle clip) perché
  cambia il contratto del produttore, non la definizione dell'effetto.
- `semantic_effects.go` e `semantic_image_compile.go` si riducono di
  conseguenza; aggiunti test mirati (`semantic_group_compile_test`,
  `semantic_sfx_test`, `semantic_map_test`).

## 4. `workerlog` diviso in moduli

- `workerlog.go` perde ~599 righe e diventa la facciata del pacchetto; il
  contenuto si distribuisce in `workerlog_bridge.go`, `workerlog_job.go`,
  `workerlog_joblog.go` e `workerlog_sink.go` (fan-out sink, writer con cap di
  dimensione, renderer di riga unico condiviso dai due handler).

## 5. Workspace lock consolidato

- `workspace_lock_marker.go` possiede la regola di recupero di un marker il cui
  processo proprietario è assente, condivisa dai fallback GOOS esotici e
  Windows; `processExists` resta la sonda di piattaforma, volutamente
  conservativa.
- Rimossi `workspace_lock_linux.go` e `workspace_lock_windows.go`; restano
  `workspace_lock_unix.go` e `workspace_lock_other.go` sulla stessa regola.

## 6. Coda e object store

- `queue/client/httpclient.go` nuovo: il client HTTP streaming è estratto da
  `client.go` e usato da `New`, così il budget di attesa lungo non viene più
  tappato dal vecchio timeout client di 30 s; le fasi connect/TLS/response
  header restano limitate singolarmente.
- Repository Postgres riorganizzato: nuovo `artifact_rows.go`, e
  `lifecycle.go`, `finalize.go`, `children.go`, `by_parent.go`,
  `repository.go` riallineati; memoria e model aggiornati di conseguenza.
- Route server di claim/submit/transition e worker riallineate al nuovo
  contratto; `objectstore/internal/server/server.go` unifica la lettura degli
  oggetti in `openObject`, con un solo contratto di errore condiviso da GET e
  HEAD (404 assente, 400 content address malformato, 500 il resto) e streaming
  senza buffering.

## 7. Harness di certificazione e fix autofit

- Le lane di certificazione sono state ri-ospitate: `certification_harness_test.go`
  perde ~983 righe a favore di `editorial_v1_golden_harness_test.go` e
  `final_certification_harness_test.go`, con lane editorial/final/goal3.
- Corretto l'autofit del ritratto entità perché copra solo i box landscape:
  il golden canary torna verde (come dichiarato nel commit).
- `CONFORMANCE.md` aggiornato con le due nuove regole e la loro motivazione;
  `infra/docker/renderinggen-worker.Dockerfile` e i test di `buildinfo`
  aggiornati di conseguenza.

## Nota

Il changelog è stato scritto a posteriori leggendo il commit della giornata e
il diff. Il commit che lo aggiunge tocca solo documentazione; come controllo
rapido sono stati rieseguiti la suite architetturale
(`go test ./internal/architecture/...`, ok) e i test camera/wrap/child
dell'overlay (`go test ./internal/overlay -run 'TestCamera|TestPhraseWrap|TestImageChild'`, ok).
