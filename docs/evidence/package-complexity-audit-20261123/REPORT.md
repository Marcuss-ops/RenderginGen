# Audit complessità package — RenderingGen

**Data:** 2026-11-23
**Scope:** i tre moduli Go del repository (`renderinggen`, `queue`, `objectstore`),
con focus sui package interni più grandi.
**Metodo:** `go run tools/archaudit/main.go` (AST Go, complessità a conteggio di
rami, riferimenti tra file), censimento delle funzioni non-test per righe
fisiche e `go list` per le dipendenze interne. Le misure sono state prese sul
checkout `c511592` e riverificate dopo il primo incremento.

## Esito sintetico

| Priorità | Package | LOC non-test | File non-test | Segnale principale | Decisione |
|---|---|---:|---:|---|---|
| P1 | `renderinggen/internal/overlay` | 23.637 | 90 | 247 file Go totali (157 di test), 1.610 funzioni; hotspot puri e harness di test giganteschi | Bonifica per funzioni e harness, **non** split del package |
| P1 | `renderinggen/internal/processor` | 7.943 | 46 | `storeArtifact` 240 righe e 16 parametri; `prepareWorkspace` 183; `ImportOverlayArtifact` 162 | Prossimo refactor strutturale: input struct + fasi nominate |
| P2 | `renderinggen/internal/chronon` | 4.116 | 17 | `telemetry.go` 609, `client.go` 534, `daemon_pool.go` 447; `Client.Render` 152 | Split solo se si tocca il trasporto; non è un package da spezzare per dimensione |
| P2 | `renderinggen/internal/motion` | 2.886 | 11 | `types.go` 685; `ValidateDefinition` 84 righe / complessità 49 | Validazione per famiglia, non nuovo package |
| P2 | `queue/internal/repository/postgres` | 2.639 | 14 | `claimN` 180, `getArtifact` 151, `repository.go` 869 | Solo con test di parità PostgreSQL; rischio semantico alto |
| P3 | `renderinggen/internal/storage` | 1.910 | 8 | cache L1/L2 e integrità già coperte da test | Nessun intervento ora |
| P3 | `renderinggen/internal/media` | 1.528 | 8 | `gop_probe.go` e probe ffprobe | Nessun intervento ora |
| P3 | `objectstore/internal/store` | 1.092 | 6 | staging e LRU già bonificati dall'audit 2026-11-10 | Nessun intervento ora |

Il repository RenderingGen contiene 761 file Go; `overlay` da solo pesa circa
un terzo delle righe non-test dei tre moduli. Non è però un package con
un confine sbagliato: è il compilatore semantico unico, e i suoi tipi di piano
sono referenziati da 182 file (`plan_model.go`). Spostarli in sottopackage ridurrebbe solo la
leggibilità del percorso, non l'ampiezza del cambiamento.

> Nota di lettura: le tabelle degli hotspot nelle sezioni 1–5 descrivono lo
> stato **pre-intervento**. Il censimento corrente è nella sezione “Terzo
> incremento”; dopo la wave 3 non restano funzioni non-test ≥100 righe nei sei
> package `renderinggen` osservati.

## 1. `overlay` — il package più complesso

### Perché non si spezza il package

- `plan_model.go` ha 2.285 usi in ingresso da 182 file; `compiler.go` 391 usi da
  114 file. Il contratto del piano è il vocabolario condiviso del compilatore:
  spostarlo in un sottopackage obbligherebbe ogni consumer a cambiare import,
  senza ridurre il numero di file toccati da una regola nuova.
- Il commit `c511592` ha già piegato i compilatori per feature sui seam dei
  `kind`. `semantic_map.go` e `semantic_map_modern.go` sono i file proprietari
  del kind mappa, non due moduli indipendenti.
- Il problema misurato non è il confine del package ma la concentrazione di
  responsabilità dentro poche funzioni e la duplicazione degli harness di test.

### Hotspot residui (non-test)

| Funzione | File | Righe | Complessità |
|---|---|---:|---:|
| `modernMapOverlayLayers` | `semantic_map_modern.go` | 171 | 45 |
| `compilePremiumComponent` | `premium_image_recipe.go` | 161 | 48 |
| `compileEntityCard` | `semantic_entity_compile.go` | 154 | — |
| `parseStyleBlock` | `visual_style_resolver.go` | 153 | 44 |
| `compileImageCaptionLayer` | `semantic_image_effects.go` | 149 | — |
| `validateSceneCamera` | `scene_camera_rig.go` | 90 | 52 |
| `validateMapTextStyle` | `semantic_map.go` | 45 | 52 |
| `frameFromImageParams` | `semantic_frame.go` | 96 | 51 |

I primi cinque sono i candidati giusti per la prossima tornata: hanno fasi
riconoscibili (normalizzazione input → validazione → costruzione layer) e test
già esistenti nel package. `validateSceneCamera`, `validateMapTextStyle` e
`frameFromImageParams` sono funzioni pure ad alta ramificazione: si possono
spezzare e testare senza toccare il contratto del piano.

### Test harness

I file di test dominano la manutenzione: `shape_comprehensive_suite_test.go`
1.709 righe, `overlay_v3_catalog_test.go` 802, `catalog_motion_gpu_test.go` 673.
Le tre funzioni di test con complessità più alta sono
`TestRuntimeAnimationUseCasesSeparateCountedCompositions` (97),
`TestEveryCallableCatalogMotionExecutesOnTheStrictGPU` (88) e
`TestGoldenEditorialV1MegaCanary` (74). Estrarre fixture e helper condivisi è
utile, ma va fatto dopo gli hotspot di produzione: un harness estratto male
sposta il costo di modifica dai test alla produzione.

## 2. `processor` — il prossimo refactor strutturale

`storeArtifact` (`store_artifact.go`, 240 righe) prende 16 parametri e mescola
sei fasi:

1. identità dell'artefatto (tail upload o hash del file);
2. pubblicazione dei byte nell'object store;
3. assemblaggio dei fatti dell'artefatto (probe, output facts, segmenti);
4. ingest della telemetria Chronon e verifica della composizione;
5. conservazione del sidecar timing raw;
6. scrittura della riga di ledger.

La firma è il sintomo: il chiamante passa uno stato che la funzione conosce già
in gran parte. Il refactor consigliato è un `storeArtifactInput` esplicito e
cinque helper di fase, mantenendo l'invariante
`local_sha == objectstore_sha == db_sha` in un solo punto. È un intervento sul
percorso critico: va fatto con la suite `processor` completa e i golden, non
con una modifica meccanica.

`prepareWorkspace` (183 righe) e `ImportOverlayArtifact` (162) vengono dopo.
Il resto del package ha già una struttura per fase leggibile (`prepare_*`,
`finalization.go`, `segments.go`, `receipt_verify.go`).

## 3. `chronon` — trasporto e telemetria

`telemetry.go` (609 righe) e `client.go` (534) sono grandi ma coesi: il primo
possiede il contratto del sidecar, il secondo il trasporto CLI. `daemon_pool.go`
(447) è un terzo asse (pool e socket). `Client.Render` (152 righe) è il punto
più lungo del percorso. Un split del package non è giustificato oggi; se si
interviene, la separazione naturale è `telemetry` (parsing e schema) dal client
(transport), non una divisione per dimensione dei file.

## 4. `motion` — validazione, non struttura

`types.go` (685 righe) contiene tipi e validazione delle famiglie;
`ValidateDefinition` (84 righe, complessità 49) è il punto da spezzare per
famiglia (`image_group`, `premium`, `visual_accents`). Il package è già un
registry di dati: non va spostato in sottopackage e non va frammentato per
ottenere file più corti.

## 5. `queue/postgres` — rischio DB

`claimN` (180 righe) e `getArtifact` (151) sono i due hotspot. La logica di
claim, lease generation e idempotenza è già coperta da test di parità tra
backend memory e PostgreSQL. Qualsiasi estrazione deve preservare le query e
l'ordine degli eventi; senza `TEST_DATABASE_URL` la verifica è incompleta.

## Primo incremento eseguito (2026-11-23)

`parseGradient` (`shape_colors_gradients.go`) era la funzione non-test con la
complessità più alta del repository: 138 righe, complessità 64. È stata divisa
in fasi nominate:

| Funzione | Righe | Responsabilità |
|---|---:|---|
| `parseGradient` | 38 | orchestra le fasi e applica il check finale |
| `parseGradientObject` | 14 | oggetto e vocabolario delle chiavi |
| `parseGradientType` | 11 | `linear` / `radial` / `conic`, default `linear` |
| `parseGradientEndpoints` | 19 | `start` / `end` con default |
| `parseGradientColorStops` | 9 | scelta tra i due dialetti |
| `parseGradientTupleStops` | 25 | forma `stops: [[color, position], ...]` |
| `parseGradientObjectStops` | 35 | forma `color_stops: [{color, position}]` |
| `parseGradientOpacityStops` | 34 | `opacity_stops` |

Non è cambiato il comportamento osservabile: stessi testi d'errore, stessa
precedenza (tipo → conflitto dialetti → endpoint → color stops → opacity stops →
check finale), stesso `Stops` raw usato dal marshalling. Il file cresce di 30
righe (270 → 330), ma la funzione massima scende da 138 a 38 e ogni fase è
testabile da sola.

Aggiunto `shape_gradient_parse_test.go` (non tag-gated): copre i default, la
parità tra `stops` e `color_stops`, la preservazione dei colori raw e 19 casi
d'errore. I test gradient preesistenti vivono nella suite `certification` e non
giravano nel gate veloce.

### Verifica

| Comando | Esito |
|---|---|
| `gofmt -l` sui due file toccati | vuoto |
| `go test ./internal/overlay/ -run 'TestParseGradient' -count=1` | ok |
| `go test -tags certification ./internal/overlay/ -run 'Gradient' -count=1` | ok |
| `go test ./internal/overlay/ -count=1` | ok (2,406 s) |
| `go build ./...` | exit 0 |
| `go vet ./internal/overlay` | exit 0 |

## Secondo incremento (2026-11-23): azzerate le funzioni ≥150 righe

Dopo il primo incremento restavano nove funzioni non-test sopra le 150 righe
nel modulo `renderinggen`. Sono state tutte decomposte in fasi nominate, senza
cambiare comportamento osservabile, testi d'errore o precedenza dei controlli.

| Funzione | Prima | Dopo | Estratti principali |
|---|---:|---:|---|
| `modernMapOverlayLayers` | 171 | **38** | motion, piazzamento pin, layer pin, route, map_fx |
| `compilePremiumComponent` | 161 | **14** | box, layer base, payload, stato, canvas, effetti |
| `compileEntityCard` | 154 | **29** | stile, motion, immagine, caption |
| `parseStyleBlock` | 153 | **48** | campi tipizzati, stroke, shadow |
| `storeArtifact` | 240 | **36** | input struct + identità, publish, artifact, segmenti, telemetria |
| `prepareWorkspace` | 183 | **31** | lease/log, materialize, recompile, prescale, validazione |
| `ImportOverlayArtifact` | 162 | **42** | validazione job, workspace, identità, probe, artifact |
| `Client.Render` | 152 | **56** | processo CLI, streaming output, esito |
| `probeClosedGOP` | 177 | **38** | parsing streaming della packet table |

Il censimento post-intervento sul modulo `renderinggen` riporta **zero
funzioni non-test ≥150 righe** in `overlay`, `processor`, `chronon`, `media`,
`storage` e `motion`. Il helper più lungo introdotto è
`applyPremiumComponentPayload` (68 righe); `readGOPPacketTable` resta a 128
righe, sotto la soglia e con una sola responsabilità.

Il gate `deadcode` ha inoltre individuato due funzioni morte preesistenti in
`overlay/chain_pan.go` (`layerAnimationTrack` e `chainPanAnimation`): non erano
raggiungibili da codice né da test, quindi sono state rimosse. Il gate ora
riporta solo le quattro eccezioni di contratto esterno già documentate in
`internal/architecture/deadexports_test.go`.

### Verifica del secondo incremento

| Comando | Esito |
|---|---|
| `go test ./internal/overlay/ ./internal/processor/ ./internal/chronon/ ./internal/media/ -count=1` | tutti ok (2,542 s / 40,136 s / 1,606 s / 3,885 s) |
| `go test ./internal/processor/ -count=1` | ok (40,467 s) dopo `storeArtifact` |
| `go build ./...` | exit 0 |
| `go vet ./internal/overlay ./internal/processor ./internal/chronon ./internal/media` | exit 0 |
| `gofmt -l` sui quattro package | vuoto |
| `go test -race` sui quattro package | tutti ok (19,888 s / 115,129 s / 3,740 s / 10,283 s) |
| `make test-gofmt` | clean |
| `make test-architecture` | ok (25,631 s) |
| `make test-unit` | ok su `renderinggen`, `queue`, `objectstore` |
| `make test-module-standalone` | ok |
| `make deadcode` | clean (solo allowlist di contratto esterno) |

Residuo misurato: solo `queue/internal/repository/postgres` conserva due
funzioni ≥150 righe (`claimN` 180, `getArtifact` 151). Non sono state toccate in
questa wave perché la verifica di parità richiede PostgreSQL reale
(`TEST_DATABASE_URL`), non disponibile in questa sessione.

## Terzo incremento (2026-11-23): azzerate le funzioni ≥100 righe

Obiettivo della wave 3: portare a zero le funzioni non-test con almeno 100
righe nei sei package già bonificati (`overlay`, `processor`, `chronon`,
`media`, `storage`, `motion`). Il criterio è verificato con lo stesso
`tools/archaudit` e con un filtro che esclude i file `_test.go`.

| Funzione | Prima | Dopo | Estratti principali |
|---|---:|---:|---|
| `compileImageCaptionLayer` | 149 | **57** | motion, geometria, style params |
| `resolveAnimationGroup` | 141 | **40** | header, claim member, motion, finestra |
| `ResolveEntityCardLayoutAtSize` | 135 | **13** | base/safe, side, below |
| `resolvePlan` | 126 | **54** | shell, background, asset, source |
| `compileResolvedMapLayers` | 111 | **34** | basemap, route, pin, camera |
| `applyWorldMapParams` | 107 | **53** | projection, center, highlight, route, dots |
| `compileBackgroundLayer` | 104 | **14** | validate, build |
| `semanticItem.UnmarshalJSON` | 103 | **11** | decode, timing, contract |
| `textBackgroundPlate` | 103 | **81** | motion del plate |
| `applyTextRoleBaseStyle` | 102 | **14** | phrase/caption/data/map |
| `compileResolvedText` | 101 | **19** | build layer, geometria, animazione |
| `ParentFinalizer.finalize` | 134 | **41** | claim, lease, output, input, assemble, publish |
| `Processor.FinalizeJob` | 126 | **52** | canonical+segmenti, probe |
| `probeFile` | 143 | **18** | documento, stream, keyframe, frame count |
| `parseCanonical` | 113 | **24** | header, sezioni, motion, preset, selezioni |

Il censimento finale riporta **zero funzioni non-test ≥100 righe** in tutti e
sei i package. La funzione più lunga tra quelle toccate è `textBackgroundPlate`
(81 righe). Al momento di questa misura `queue/internal/repository/postgres`
era l'unico residuo sopra 150 righe; la coda locale descritta sotto lo ha
chiuso con PostgreSQL reale.

### Verifica del terzo incremento

| Comando | Esito |
|---|---|
| `go test ./internal/overlay -count=1` | ok (1,969 s) |
| `go test ./internal/processor -count=1` | ok (43,968 s) |
| `go test ./internal/media ./internal/motion -count=1` | ok (3,343 s / 0,193 s) |
| `go test -race ./internal/overlay ./internal/processor ./internal/media ./internal/motion -count=1` | ok (16,683 s / 93,730 s / 7,019 s / 2,938 s) |
| `go build ./...` | exit 0 |
| `go vet ./internal/overlay ./internal/processor ./internal/media ./internal/motion` | exit 0 |
| `gofmt -l` sui sei package | vuoto |
| `make test-gofmt` | clean |
| `make test-architecture` | ok (38,115 s) |
| `make test-unit` | ok su `renderinggen`, `queue`, `objectstore` |
| `make test-module-standalone` | ok |
| `make deadcode` | clean (solo allowlist di contratto esterno) |
| `git diff --check` | clean |

## Coda locale (2026-11-23): PostgreSQL reale e hotspot overlay

### Queue: parità PostgreSQL e zero funzioni ≥100

La coda non era chiudibile con i soli test memory. Ho avviato PostgreSQL 18.6
in Docker (`pgvector/pgvector:pg18`, porta 55432) e ho eseguito la suite con
`TEST_DATABASE_URL` e `RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1`. La baseline ha
esposto un bug reale: `persistArtifactTx` scartava un artifact con hash ma senza
`StorageKey`, quindi il job risultava completato senza riga artifact. La guardia
ora persiste gli artifact con hash e lascia `model.Artifact{}` come segnale di
"niente da persistere"; il conformance è verde.

| Funzione | Prima | Dopo |
|---|---:|---:|
| `Repository.claimN` | 180 | **43** |
| `getArtifact` | 151 | **15** |
| `Repository.Get` | 108 | **96** |

Il package `queue/internal/repository/postgres` non ha più funzioni non-test
≥100 righe. Verifiche: `go test ./...` con PostgreSQL obbligatorio (10,314 s),
`go test -race ./internal/repository/postgres` (13,603 s), `go vet ./...` ok.

### Overlay: hotspot ciclomatici

| Funzione | Prima righe/complessità | Dopo |
|---|---:|---:|
| `validateSceneCamera` | 90 / 52 | **18 / 6** |
| `validateMapTextStyle` | 45 / 52 | **15 / 5** |
| `frameFromImageParams` | 96 / 51 | **31 / 12** |

Le fasi sono ora nominate (nodo/lente/DOF/rig/depth, blocchi tipografici,
border/stroke/shadow/radius). Errori, ordine dei controlli e fallback restano
identici. `go test ./internal/overlay` ok (2,548 s); `-race` ok (17,826 s).

### Harness overlay e closeout

`shape_comprehensive_suite_test.go` e `overlay_v3_catalog_test.go` non
condividono fixture: `compileSingleShape` è già in
`final_certification_harness_test.go`, mentre gli helper V3 sono locali alla
suite. Non ho forzato uno split senza duplicazione reale.

`ops/tools/verify_closeout.sh` esiste, `bash -n` e `--help` sono verdi. La run
`--skip-live --only 1,2,4` non è conclusa in 300 s perché `make verify-agent`
resta attivo; `refactored` ha 421 file sporchi. Il report precedente resta
`PASS=13 FAIL=2 BLOCKED=1`: CLOSE-04 non è chiudibile senza un tree pulito.

### Ticket residui

Il registro `docs/tickets/OPEN.md` è stato rigenerato: riporta **14 ticket non
chiusi** (1 BLOCKED, 7 OPEN, 6 PARTIAL), non più 24. Le schede verificate
mostrano:
- 6 GOP aperti nel repo `refactored` (01, 02, 03, 09, 13, 14): richiedono
  refactor multi-PR e il tree è già sporco di 421 file;
- CR-19-23, PAR-01, IMG-05, E2E-03/04, ELIM-03, BONIFICA-07: richiedono GPU,
  Master, Drive, provider live o payload reali;
- CLOSE-04: harness presente, ma clean-tree/verify-agent non verdi.

Nessuno stato è stato cambiato senza comando, exit code e artefatto.

## Prossimi passi, in ordine

1. **Sblocco closeout**: riportare `refactored` a un tree pulito e concludere
   `verify_closeout.sh --skip-live`; solo allora CLOSE-04 può passare a CLOSED.
2. **GOP**: affrontare i sei ticket aperti uno per PR, partendo da GOP-14/13
   (headroom) e GOP-09; GOP-01/02/03 richiedono più stadi.
3. **Esterni**: CR-19-23, PAR-01, IMG-05, E2E-03/04, ELIM-03, BONIFICA-07
   richiedono i prerequisiti dichiarati nei rispettivi ticket.
4. **Test harness**: nessuna estrazione forzata; se in futuro emergono fixture
   duplicate, il punto di ingresso è `final_certification_harness_test.go`.

## Guardrail

- Nessun cambiamento osservabile: testi d'errore, precedenza dei controlli,
  ordine delle transazioni e invarianti di hash restano quelli attuali.
- Non spostare `compiler.go`, `plan_model.go` o i tipi del piano: il costo di
  import supera il beneficio e il contratto è già condiviso.
- Non spezzare un file solo perché supera le 1.000 righe: `semantic_map.go` e
  `semantic_map_modern.go` sono kind-owned e la loro dimensione è l'elenco delle
  regole della mappa, non un confine di modulo.
- Ogni tornata si chiude con `gofmt`, build, vet e test del package toccato;
  la suite completa e i gate repo-wide (`test-unit`, `deadcode`,
  `test-architecture`) si eseguono prima della chiusura della wave.

## Riproduzione delle misure

```sh
cd RenderingGen
go run tools/archaudit/main.go -dir renderinggen/internal/overlay -top-funcs 20
go run tools/archaudit/main.go -dir renderinggen/internal/processor -top-funcs 20
go run tools/archaudit/main.go -dir renderinggen/internal/chronon -top-funcs 20
```
