# RenderingGen — Analisi cleanup (6 ottobre 2026)

Analisi del peso e backlog di cleanup per ridurre complessità senza indebolire
contratti, test o prove. Fotografia del checkout del 6 ottobre 2026: conteggi
riproducibili sui file Go tracciati, salvo dove è indicato il disco locale. Il
backlog distingue fatti verificati da ipotesi e non autorizza cancellazioni,
riscritture della storia Git o modifiche di deployment.

**Obiettivo operativo:** ridurre artefatti ridondanti e duplicazioni reali,
conservando una via verificabile per riprodurre i render e mantenendo intatti
contratti e gate. Prima si correggono le misure; poi si affrontano solo le fasi
con criteri di accettazione espliciti.

**Stato:** rimozioni e verifiche degli asset concluse; inventario completo dei
128 percorsi MP4 baseline. Tutti i gate repository eseguibili sono verdi dopo
aver riallineato il contratto semantic e mantenuto interni i picker senza
consumer. Restano decisioni non repository-verificabili su retention e
sign-off owner dei profili deployment; non sono state presunte.

---

## Piano di lavoro (ordine e criteri di completamento)

- [x] Verificare conteggi, directory, configurazioni e package sul checkout.
- [x] Correggere affermazioni non supportate e distinguere dimensioni locali,
      oggetti tracciati e pack Git.
- [x] Definire ordine, prerequisiti e acceptance criteria per ogni cleanup.
- [x] Completare la revisione statica di manifest, config e comandi prima di proporre refactor.
- [x] Eseguire i gate finali sul worktree: `make test-unit`, `make test-gofmt`, `make test-architecture`, `make conformance`, `make test-module-standalone`, `go vet` su tutti i moduli, test batch mirati e `git diff --check`.
- [x] Verificare con smoke reali i consumer `batch-submit -dry-run` e `batch-render-presets -dry-run`, senza submit né render.
- [x] Audire simboli esportati e funzioni private non test; i picker helpers senza consumer sono interni al package, il ratchet `TestNoDeadExports` passa senza nuove eccezioni.
- [x] Esaminare i test segnalati come storici/legacy: non emerge una suite sostituita o priva di contratto; non cancellare test solo per età o dimensione.
- [x] Rimuovere 30 MP4 piatti v4 soltanto dopo aver verificato per ognuno esatta parità SHA-256 con la copia canonica referenziata dal report v4 e assenza di riferimenti al vecchio path.
- [x] Aggiornare il CSV inventory per conservare i record e la ragione delle 31 rimozioni (30 copie identiche + 1 partial vuoto).
- [ ] Owner: confermare retention/archive verificato per le restanti copie/evidenze; senza prova di storage alternativo, conservarle nel repository.

Le rimozioni e i gate repository sono conclusi senza cambiare deployment o
contenuti delle prove. Non dichiaro chiuse le decisioni esterne: mancano la
conferma di retention per le evidenze restanti e il sign-off owner sui profili
worker. Nessun test è stato soppresso e nessuna modifica preesistente è stata
scartata.

---

## Riepilogo delle misure verificate

| Misura | Valore | Ambito |
|---|---:|---|
| File Go / LOC RenderingGen | 346 / 75.940 | 37.367 prod + 38.573 test |
| File Go / LOC queue | 93 / 13.449 | 6.593 prod + 6.856 test |
| File Go / LOC objectstore | 9 / 2.212 | 1.019 prod + 1.193 test |
| LOC Go totali / quota test | 91.601 / 50,9% | `wc -l`, incluse righe vuote e commenti |
| File tracciati | 1.018 | `git ls-files` |
| MP4 / log tracciati | 128 / 144 | tutti i percorsi nel repository |
| MP4 nei due corpus (baseline → presenti) | 121 → 91 | 106 multilingual + 15 Tyson; rimossi 30 duplicati v4 byte-identici, restano 76 multilingual + 15 Tyson |
| Pack Git | 108,92 MiB | `git count-objects -vH`; include la storia, non è attribuibile ai soli file correnti |
| Config YAML worker | 7 | 1 `renderinggen/config.yaml`, 2 native, 4 Docker (CI incluso); `config.Load` li carica e il test dei config shipped passa |
| Package/manifest batch | 2 submission + 1 corpus + 1 preset-matrix | `batch` possiede flat+multilingual; `overlaybatch` riusa flat; `renderbatch` ha un contratto preset distinto e un writer dei piani condiviso |
| Config drift | misurato, non solo ipotizzato | differenze effettive includono profilo software CI, `gpu_lanes` 1/2/3, socket/cache/workspace, Drive e opzioni specifiche della config principale; i 7 file espongono da 15 a 38 foglie YAML e usano default per le altre impostazioni |
| Comandi gallery/scenario documentati | 2 script scenario | `run-vidrush-scenario.sh` e `run-mixed-overlay-scenario.sh` invocano comandi che non vanno rimossi senza migrare gli entry point |
| Directory in `renderinggen/cmd` | 19 | 15 con sorgenti Go, 4 vuote nel checkout |

La dimensione del pack non si somma alle dimensioni dei file correnti; rimuovere
un file dal prossimo commit non elimina le sue versioni dai commit precedenti.
Le directory locali vuote non sono file Git e non costituiscono target Go.

---

## 1. Quanto è grasso (numeri misurati)

### Codice Go — 3 moduli, 91.601 LOC totali

| Modulo | File Go | Prod | Test | Totale |
|---|---:|---:|---:|---:|
| `renderinggen/` (worker) | 346 | 37.367 | 38.573 | 75.940 |
| `queue/` | 93 | 6.593 | 6.856 | 13.449 |
| `objectstore/` | 9 | 1.019 | 1.193 | 2.212 |
| **Totale** | **448** | **44.979** | **46.622** | **91.601** |

→ **50,9% sono righe in file di test**, non automaticamente codice superfluo.

### Il peso è concentrato in pochi punti

- **`internal/overlay`: 24.358 LOC su 103 file Go, 9.839 prod + 14.519 test.**
  Include compiler/lowering, preset e helper geometrici, ma anche test di
  certificazione. I 2.472 LOC di `final_certification*`, `goal3_*` e
  `catalog_motion_gpu_test.go` sono tutti test: spostarli non riduce il codice
  incluso nel binario e può rompere l'accesso a simboli non esportati. È un
  problema di organizzazione/esecuzione dei test, non una prova di “god package”
  di runtime. La stima «339 funzioni esportate / 101 tipi» non è confermata e
  viene ritirata.
- **Git:** 1.018 file tracciati nel baseline, pack di 108,92 MiB. In tutto il repo
  risultano tracciati 128 MP4 (136.881.580 byte di blob non compressi) e 144
  `.log` (1,7 MB). Nei due corpus nominati: `multilingual_overlays_v1/` ha 257
  file, 106 MP4 e 5 log; `mike_tyson_overlay_test/` ha 55 file e 15 MP4.
  I 121 MP4 dei due corpus sommano **75.860.504 byte** al baseline; nel worktree
  ne restano 91 per **57.961.546 byte** dopo aver rimosso 30 duplicati v4
  (risparmio verificabile: **17.898.958 byte**). L'inventario
  `CLEANUP_ARTIFACT_INVENTORY.csv` conserva dimensioni, hash, metadati `ffprobe`,
  classe, decisione e motivazione per ciascuno dei 128 percorsi MP4 baseline:
  97 presenti e 31 rimossi (incluso il partial da 0 byte). Dei 67 percorsi
  eccedenti in gruppi SHA-256 identici al baseline, 30 sono stati rimossi; 37
  restano perché il percorso è evidenza o referenziato da report/snapshot.
  `baseline_paths_with_same_sha256` conta i percorsi nel baseline originario,
  incluso quello della riga. I test e i gate finali dopo le rimozioni sono
  registrati in sezione 7. `du` sul checkout
  misura anche output ignorati/generati: non va presentato come peso in Git.
  `renderinggen/motion-certification/latest/` contiene inoltre 139 log
  tracciati. `.gitignore` già esclude diversi output generati, ma non tutti i
  render certificati che sono stati aggiunti al tracking.
- **`cmd/`: 19 directory**, 15 con sorgenti Go e 4 vuote/non tracciate. Queste
  ultime non sono binari o package Go da eliminare dal repository. I comandi
  gallery/scenario pieni di codice sono invece strumenti reali; consolidarli
  richiede una valutazione di uso e compatibilità, non basta contare directory.

---

## 2. Perché è difficile

1. **Gerarchia concettuale a più livelli:** sezione editoriale → `kind` →
   template/preset → motion → renderer. I conteggi documentati descrivono
   livelli diversi e si sovrappongono; non sono somme di componenti eliminabili.
2. **Cataloghi e registri con proprietari distinti:** i motion vengono caricati
   da cataloghi JSON embedded (ChrononTemplate, 728.273 byte, e short-phrase,
   94.762 byte); le famiglie di preset del worker sono controllate contro il
   catalogo con `ValidateCatalogParity`. È una verifica intenzionale tra
   componenti/owners, non una lista unica attraverso tutti i domini.
3. **Contratto semantic duplicato intenzionalmente:**
   `contracts/overlay-plan.v1.schema.json` e le struct Go in `compiler.go` sono
   presidiate da parity test bidirezionali. Codegen può rimuovere la duplicazione,
   ma deve preservare il contratto chiuso, i tag JSON e i tipi/punti custom.
4. **Configurazione:** ci sono 7 file di configurazione worker (non 9). Le
   differenze misurate non sono solo endpoint/path/`gpu_lanes`: CI usa il
   profilo software, Drive ha opzioni dedicate e i worker native A/B hanno
   socket, workspace e cache separati. La config principale aggiunge diversi
   valori operativi che mancano nei profili più minimali. `config.Load` usa
   `KnownFields(true)`, applica override ambiente e default; non risulta
   supportato un include/merge YAML. `strict_native_backend` omesso nei
   profili GPU non equivale a false: il default lo abilita dal profilo.
   `TestShippedConfigsLoad` carica già i 7 file e il test mirato passa: il
   prossimo lavoro utile è motivare le differenze, non aggiungere un altro
   controllo statico dei soli YAML grezzi.
5. **Gate di conformance:** il controllo architetturale e i test proteggono
   confini reali; prima di semplificarli va dimostrato che i vincoli restano
   applicati, non solo che la suite passa localmente.

---

## 3. Cosa è GIÀ condiviso (non duplicare)

- **Identità motion:** gli ID sono caricati dai cataloghi JSON invece di essere
  mantenuti in una seconda lista Go; catalogo breve e catalogo ChrononTemplate
  restano due asset/versioni distinti da riconoscere.
- **Manifest di submission e corpus:** `overlaybatch` legge il manifest flat
  che `internal/batch` espande per `cmd/batch-submit`; i campi extra del report
  vengono letti separatamente e non sono una seconda espansione di job.
  `batch` ha inoltre un contratto multilingual separato, protetto da parity test.
- **Builder batch:** i job del corpus incorporano `batch.FlatJob` per gli
  stessi campi wire; i byte emessi vengono decodificati da `batch.Decode`
  prima che manifest e sidecar siano scritti.
- **Piano di render:** `renderbatch.BuildPlan` è un writer tipizzato usato anche
  da comandi scenario/gallery; non va inglobato o rimosso solo perché esiste un
  manifest benchmark separato.
- **Renderer:** Chronon3D è consumato come runtime esterno, non vendorizzato.
- **Queue:** package separato, con responsabilità chiare; i suoi ~13,4k LOC
  non sono un target di cleanup sulla base dei conteggi attuali.

---

## 4. Backlog di cleanup (priorità e acceptance criteria)

### ① Render e prove: inventariare prima, rimuovere solo dopo

**Fatto verificato:** `CLEANUP_ARTIFACT_INVENTORY.csv` contiene ogni MP4
originariamente tracciato (128 righe dati), con percorso, classe, byte, SHA-256,
metadati media e motivazione. Alla verifica finale, `ffprobe` e SHA-256 passano
per tutti i 97 media ancora presenti; tutti i 121 render corpus del baseline
sono stati validati come H.264 1920×1080 di 5 secondi prima delle rimozioni.
Il CSV distingue i 97 file presenti dai 31 rimossi dal worktree; l'output JSON
temporaneo dell'audit non è stato committato. Il `du` locale è più grande perché include output generati e
ignorati; il repository ha già regole `.gitignore` per varie directory di output.
La proposta «~-300 MB dal repo» è ritirata. Rimuovere i file dal tip del branch
riduce lo spazio del working tree al tip, ma non il pack della storia né la
dimensione di un clone completo; per ridurre il clone servirebbero shallow/filter
clone o una riscrittura Git separata, non inclusa nel backlog autorizzato.

**Classificazione effettuata:** gli MP4 in `assets/backgrounds/` sono sorgenti
usate dalle pipeline; `testdata/golden/` è fixture canonica; i render Tyson e
multilingual sono evidenze/corpus con report e sidecar. Un sottoinsieme v4 piatto
(30 files) era duplicato esattamente da `extractor_phrase_artifacts_v4/phrases/phrases/`:
per ogni file il canonical è referenziato da `extractor_phrase_report_v4.json`,
mentre il path piatto non compariva in alcun altro file testuale. Solo le 30 copie
piatte sono state rimosse; copie duplicate con report/snapshot consumer sono
rimaste. Per tutte le rimanenti prove non c'è verifica di archivio esterno/hash.

**Accettazione:** inventario completo dei 121 MP4 corpus baseline con classe e
decisione; duplicati eliminati solo dopo prova hash/percorso; hash e metadati
dei media presenti verificati; nessun test fa riferimento ai path rimossi.
Inventariati tutti i 128 percorsi MP4 baseline. Sono stati rimossi un partial
0 byte invalido e 30 copie v4 esatte: per ciascuna è presente la copia canonica,
referenziata dal report, e il vecchio path non ha consumer testuali fuori
inventario. Nessun JSON/report è stato modificato. I restanti artefatti non sono
rimossi in assenza di retention esterna verificata e sign-off sul path di record.
Nessuna riscrittura della storia Git. Un eventuale `git filter-repo` per ridurre
i cloni è attività separata, coordinata e soggetta ad autorizzazione esplicita.

### ② Manifest batch: misurare l'overlap, non forzare uno schema unico

I pacchetti non sono tre manifest equivalenti: `batch` gestisce flat e
multilingual (`renderinggen.batch-manifest.v1` e
`renderinggen.batch-multilingual.v1`); `overlaybatch` costruisce/esegue batch
sullo schema flat condiviso e aggiunge metadati di report; `renderbatch` ha un
manifest per le matrici preset (`renderinggen.preset-render-manifest.v1`) e
fornisce anche il writer tipizzato dei piani usato da altri comandi.

**Passi già verificati:** `overlaybatch.Run` legge i metadati per il report e
poi passa gli stessi byte a `batch.Decode`; la submission è espansa solo da
`internal/batch`. La parità con schema pubblicato è verificata per il contratto
multilingual, non per quello flat. `renderbatch` usa un contratto separato per
la matrice preset, mentre `BuildPlan` è riusato dai comandi gallery/scenario.

**Esito:** la mappa schemi/consumer è completata. Nessuna unificazione di schema
è giustificata dalle differenze osservate; non avviare refactor finché un futuro
audit non individua una regola concreta duplicata.

**Accettazione:** test di compatibilità per tutte le versioni; stessi job ID,
chiavi idempotenti, asset e report sui fixture esistenti; i builder preparano i
piani e falliscono prima di avviare render; `go test` dei tre package e dei
comandi consumatori passa. Nessuna modifica semantica ai manifest pubblicati.

### ③ Config worker: ridurre drift mantenendo profili espliciti

Inventario completato sui sette file nominati dal test `TestShippedConfigsLoad`:
valori espliciti appiattiti, profilo, default loader e consumer. Il processo non
aveva variabili `RENDERINGGEN_*` impostate durante l'analisi; gli override sono
però applicati globalmente da `config.Load` dopo YAML e prima dei default, con
alias legacy testati. Non introdurre `worker.base.yaml` con ereditarietà implicita:
`config.Load` decodifica un singolo YAML rigoroso e non supporta include/merge.

**Passi già verificati:** il test `TestShippedConfigsLoad` carica tutti i 7
file; `config.Load` applica override ambiente e default e rifiuta campi ignoti.
La scansione dei valori YAML trova differenze effettive in profilo, `gpu_lanes`,
endpoint, socket/cache/workspace e opzioni CI/Drive/config principale; non prova
da sola quali siano intenzionali.

**Esito parziale:** inventario delle differenze completato. Le ragioni operative
sono documentate per differenze come CI software, worker B con socket/workspace/cache
separati e Drive OAuth; restano conferma esterna e validazione dei valori finali
dopo gli override ambiente. In assenza di approvazione owner, i 7 profili autonomi
sono la scelta sicura. Non introdurre merge per ridurre il numero di file.

**Accettazione:** tutti i profili correnti continuano a risolversi; i campi
intenzionalmente distinti (backend software CI, Drive, socket/cache/workspace,
porte e GPU lanes) sono espliciti; `KnownFields`, default e env override restano
verificati; nessun deploy avviato da questo cleanup.

### ④ `internal/overlay`: separare i test solo se migliora davvero il gate

I 2.472 LOC candidati sono test; spostarli non sgonfia il binario di
produzione. Prima verificare quali test richiedono simboli non esportati,
GPU/runtime o file generati e se gravano sulla suite ordinaria.

**Passi:** etichettare i test unit/integration/GPU con la convenzione già usata
nel repo; misurare separatamente tempi e prerequisiti; solo se esiste un costo
misurato, spostare il test harness in un package/target dedicato o renderne
esplicita l'esecuzione. Evitare sottopackage di lowering finché non esiste una
necessità di riuso/evoluzione: compiler e semantica sono vincolati dal gate.

**Accettazione:** nessuna copertura persa; certificazione produce gli stessi
report/risultati; suite ordinaria e target GPU sono invocabili e documentati;
`go test ./...` e gate architetturale passano nel setup supportato. Misurare il
beneficio, senza dichiarare una riduzione LOC di produzione inesistente.

### ⑤ `cmd/`: non fissare un obiettivo arbitrario «18 → 6»

Le 4 directory vuote non sono sorgenti tracciate né binari; non richiedono
cleanup Git. I comandi gallery/scenario hanno responsabilità e asset diversi.

**Passi:** verificare invocazioni in README/script/CI e raccogliere quali
strumenti sono mantenuti; eliminare o consolidare solo comandi dimostrabilmente
orfani. Considerare `map-motion-gallery`, le gallery di frase/web e gli scenari
solo dopo un inventario di flag, input/output e dipendenze. Mantenere
`vidrush-scenario`, citato come gate compile-level, finché un sostituto non è
verificato.

**Accettazione:** nessun comando documentato o richiamato da CI/script sparisce;
per ogni comando consolidato gli esempi/flag hanno equivalenti e i render smoke
sono equivalenti; il numero di directory non è l'unico criterio di successo.

### ⑥ (Opzionale) Codegen schema → Go

Valutare solo quando il contratto cresce o il costo dei parity test è misurato.
La duplicazione attuale ha un controllo bidirezionale e impedisce sia campi
accettati ma non pubblicati sia campi di schema scartati dal worker.

**Accettazione:** generazione deterministica ripetibile in CI; copertura esatta
del set di proprietà JSON e `additionalProperties:false`; preservazione dei tipi,
`omitempty`, custom marshal/unmarshal e diagnostica; parity test rimossi solo
quando i test del generatore coprono gli stessi vincoli.

---

## 5. Verdetto

RenderingGen non appare “grasso nel design” sulla base dei conteggi soltanto.
Le opportunità verificate sono limitate e più piccole della stima iniziale:
75,9 MB di MP4 nei due corpus al baseline, con 17,9 MB rimossi come duplicati
v4 esatti; riuso già presente fra submission e batch corpus; differenze di
config effettive ma verificabili dal loader; nessun consumer del partial vuoto.
Sono stati rimossi il partial MP4 da zero byte e 30 copie v4 ridondanti. La stima
di 300 MB è falsa per i blob Git correnti e il pack da 108,92 MiB comprende
storia non eliminata da un normale commit di delete.

**Ordine conclusivo:** inventory, deduplica dimostrabile e mappa dei contratti
sono completati; la matrice config e l'audit dei consumer sono documentati.
Profiling dei test e codegen restano proposte opzionali, non cleanup provabili
con l'attuale evidenza. Nessun profilo deployment è cambiato; la retention dei
restanti output attende un owner/archive verificato.

---

## 6. Limitazioni della misura

- LOC misurato con `wc -l` sui file Go tracciati: include commenti/blank ed è
  un indicatore di volume, non di complessità ciclomatica. La percentuale di
  test è calcolata dalle righe in file `*_test.go`.
- MP4: i byte indicati sono dimensioni dei blob tracciati lette dagli oggetti
  Git (non dimensione compressa del pack); `du` misura anche artefatti locali
  ignorati. Pack Git comprende la storia disponibile e non permette di
  attribuire in modo corretto spazio corrente a un singolo percorso.
- Numeri di package/file descrivono l'albero del checkout del 6 ottobre 2026;
  eventuali modifiche locali successive possono alterare i conteggi.
- Non sono stati misurati tempi di build/test, tempo di clone, complessità
  ciclomatica o uso effettivo dei comandi; non si rivendicano miglioramenti di
  performance senza tali benchmark.

---

## 7. Avanzamento operativo e prossime azioni

Questa checklist distingue interventi verificati da proposte ancora da eseguire.
Non dichiara completato il cleanup dell'intero repository.

### Completato nei cicli incrementali

- [x] Centralizzare il lookup dei font runtime e derivare da quel registro la diagnostica.
- [x] Usare una sola mappa merged `Params`/`Style`, rispettando la precedenza di `Style`.
- [x] Semplificare il dispatch entity e l'append dei layer compositi senza cambiare l'ordine.
- [x] Rimuovere wrapper non necessari e unificare le conversioni numeriche identiche.
- [x] Unificare la validazione dei pool motion in `overlaybatch`.
- [x] Consolidare la selezione dello schema/versione render-plan in `overlay` e riusarla da serializer e writer.
- [x] Ridurre il controllo `IsCancelable` a `!IsTerminalState` e fissare la regola con test.
- [x] Condividere la validazione dei pool phrase/image e rimuovere il selettore phrase deterministico senza consumer di produzione; verificare copertura/determinismo della rotazione batch, rifiutare indici negativi e togliere `selection_seed` fuorviante dai manifest.
- [x] Rendere fail-closed `intensity` negli effetti: rifiutare valori non numerici invece di ignorare la conversione fallita e serializzare il valore originale nel piano.
- [x] Rimuovere il wrapper `enforceReceiptVerification`, usato solo dai test; coprire fast/missing, fail-closed e receipt verificato tramite `verifyArtifactReceipt`.
- [x] Ripristinare la fixture ufficiale vidrush dal file sorgente indicato da `build_entity_caption_reference_v3.py` e copiare il font Bricolage dal catalogo Chronon3d.
- [x] Correggere le aspettative del test vidrush affinché corrispondano ai due ritratti e alle due caption effettivamente dichiarate, mantenendo i controlli su animazione e collegamento immagine-caption.
- [x] Sul worktree finale `make test-unit`, `make test-architecture`, `make test-gofmt`, `make conformance`, `make test-module-standalone`, `go vet ./...` nei tre moduli e `git diff --check` sono passati; il dettaglio è in “Verifiche finali eseguite”.

### Prossime azioni precise

1. [x] Audit codice inutilizzato: i nuovi picker-helper senza callsite repository sono package-private (i test esercitano ancora la logica); il ratchet passa senza eccezioni aggiuntive. Nessuna API pubblica è stata rimossa sulla sola base di un conteggio.
2. [x] Rimozione media certa: 30 MP4 piatti v4 verificati byte-identici alle copie canoniche riportate; inventory conserva tutti e 30 i percorsi rimossi, SHA-256, stato e criterio. Il gate architecture è stato reso verde rendendo privati i picker helpers nuovi che non avevano alcun consumer nel repository.
3. [x] Mappare schemi, campi condivisi e consumer dei package `batch`, `overlaybatch` e `renderbatch`; mantenere separati i contratti con scopi diversi.
   - `internal/batch` espande `renderinggen.batch-manifest.v1` (flat) e `renderinggen.batch-multilingual.v1` in `queue.Job`, incluse ID deterministiche, validazione asset e chiavi idempotenti. Il contratto multilingual ha schema JSON check-in e parity test; non è stato trovato uno schema standalone per il flat.
   - `internal/overlaybatch` produce il manifest flat del corpus con report metadata aggiuntivi; `batch-submit`, `overlaybatch.Run` e `vramprobe` riusano `batch.Decode`. `overlaybatch.Run` e `Verify` leggono inoltre metadati report e provenienza, non una seconda espansione submission.
   - `internal/renderbatch` gestisce `renderinggen.preset-render-manifest.v1` (canvas, output, template/preset/motion), compila l'intera matrice prima del render ed è consumato da `cmd/batch-render-presets` e dai writer di piano scenario/gallery. Il file `typewriter-upload-manifest.json` è un input effettivo; non è un manifest queue-submission.
   - Nel worktree esaminato, i job aggiuntivi di `overlaybatch` incorporano `batch.FlatJob` (`id`, `render_plan`, `assets`); anche la vista `reportProbe` riusa lo stesso tipo. `writeBuild` valida i byte serializzati con `batch.Decode` prima di scrivere manifest e piani. I test e i dry-run confermano JSON, schema, campi report, job ID e idempotency key invariati.
   - Test verificati: `go test ./internal/batch ./internal/overlaybatch ./internal/renderbatch ./internal/vramprobe ./cmd/batch-build ./cmd/batch-submit ./cmd/batch-run ./cmd/batch-verify ./cmd/batch-render-presets`; smoke `batch-submit -dry-run` sul manifest Tyson tracciato (15 job, nessun submit) e `batch-render-presets -dry-run` sul typewriter (5/5 piani compilati, file temporanei rimossi); `make test-gofmt` e `git diff --check`.
   - Nessun unified manifest schema o package `batch` unico: i tre flussi mantengono responsabilità diverse. Una pipeline offline di submit è stata evitata; il comando renderbatch dry-run effettua scritture di piano per contratto e i file temporanei generati sono stati rimossi.
   - Il precedente gate aveva evidenziato due costanti `ChrononPlanVersion*` senza consumer, rese ridondanti dal wire-version resolver centralizzato; rimosse le sole costanti versione (gli schema ID usati dai test restano). Nel rilancio l'architecture gate è passato dopo aver reso privato il nuovo helper picker non usato. Nessuna eccezione ratchet aggiunta.
   - Rimosso anche `renderinggen/showcase_renders/01_documentary_background.partial.mp4`: file tracciato, vuoto (0 byte), unico elemento della directory, nessun consumer di codice/docs, impossibile da riprodurre come media.
   - Rimossi 30 MP4 in `multilingual_overlays_v1/production_rehearsal_v1/extractor_phrase_artifacts_v4/*.mp4`: ogni hash coincide con il file corrispondente in `extractor_phrase_artifacts_v4/phrases/phrases/`, i canonical sono citati nel report v4 e non esistono riferimenti testuali ai vecchi path piatti fuori dal CSV. Report, piani, sidecar e copie canoniche restano invariati.
4. [x] Completare la matrice dei 7 YAML worker: 15–38 foglie esplicite ciascuno. CI è `software-cli`/1 lane, native A/B hanno 3 lane e socket/workspace/cache dedicati, Docker default/B/Drive usano 2 lane, B/Drive workspace `/tmp` e Drive OAuth è abilitato solo sul profilo apposito. Endpoint differiscono per produzione, native e rete Compose; il loader applica i default mancanti e l’overlay env. `TestShippedConfigsLoad` + test dei default/profile/env passano. **Blocco esterno:** owner sign-off ancora necessario per dichiarare intenzionale ogni divergenza.
5. [x] Audit dei test obsoleti: cercati marker `superseded/replaced/obsolete/no longer used` nei test; i match sono spiegazioni di compatibilità/regressioni, non test senza consumer. Nessun test rimosso perché la suite preserva contratti legacy, fail-closed, rendering o gate.
6. [x] Cercare i consumer documentati dei comandi scenario: README e script invocano `vidrush-scenario`; due script invocano anche `mixed-overlay-scenario`. La ricerca limitata a documentazione/automation non ha trovato richiami ai quattro package Go vuoti né riferimenti automation delle altre gallery; l'assenza di riferimenti non basta per rimuoverli senza verifica con gli owner.
7. [x] Riparare i mismatch semantici/di build emersi nei gate e rilanciare `make test-unit`, `make test-architecture`, `make test-gofmt`, `make conformance`, `make test-module-standalone`, `go vet` nei tre moduli e test batch mirati: tutti PASS nel rilancio finale.
8. [x] Rilanciare `ffprobe` e verificare hash di tutti i 97 file MP4 presenti, inclusi i 30 path canonical conservati dopo la deduplicazione. I render Chronon/GPU non sono necessari per questa rimozione e non sono dichiarati verificati.

**Criterio per chiudere il backlog:** non basta spuntare un refactor. Ogni fase deve avere inventario/baseline, diff revisionato, acceptance criteria della sezione 4 soddisfatti e comandi di verifica con esito registrato. Le fasi che richiedono una decisione sul valore delle evidenze, retention/storage o deployment restano bloccate esplicitamente finché tale prerequisito manca.

### Verifiche finali eseguite

- `make test-unit` — PASS nei moduli `renderinggen`, `queue` e `objectstore`, incluse le prove di semantic policy, inventory motion e fail-closed.
- `make test-architecture` — PASS; nessuna eccezione/soppressione aggiunta al ratchet.
- `make conformance` — PASS (`conformance: clean (3 targets)`).
- `make test-gofmt` — PASS.
- `make test-module-standalone` — PASS: build separata di `renderinggen`, `queue` e `objectstore` senza workspace.
- `go vet ./...` — PASS nei tre moduli.
- Test batch/consumer mirati — PASS: `go test ./internal/batch ./internal/overlaybatch ./internal/renderbatch ./internal/vramprobe ./cmd/batch-build ./cmd/batch-submit ./cmd/batch-run ./cmd/batch-verify ./cmd/batch-render-presets -count=1`.
- Durante i rilanci intermedi sono stati corretti: assegnamenti per la firma a 3 valori di `motionWindows`; posizione `$defs.animation_policy` e annotazioni non supportate dal validator; riferimenti dei test ai picker ora interni; validazione compositi coerente con timing relativo dei figli; fixture e asserzioni test che confrontavano output in modo troppo rigido. Le assertion restano attive e la suite completa è stata rilanciata verde.
- Il ratchet aveva rilevato picker API non usate: i picker helper non hanno consumer repository e sono stati mantenuti package-private; i metodi Registry che hanno un uso interno restano visibili secondo necessità. Nessuna eccezione ratchet è stata aggiunta.
- `git diff --check` — PASS sul worktree finale, inclusa questa documentazione.
- CSV path parity rispetto a `git ls-files '*.mp4'` — PASS (128 righe baseline); i 97 percorsi presenti passano SHA-256 e `ffprobe`; 31 righe registrano file rimossi.
- Deduplica media: 30/30 hash identici alla copia canonica, canonical path trovato nel report v4 e nessun riferimento al vecchio path fuori dal CSV; ricontrollati hash e `ffprobe` su tutti i media presenti dopo la rimozione — PASS.
- `go test ./internal/overlay -run TestCompileShapeItem_FailClosedBounds -count=1` — PASS, incluso il caso `intensity: "loud"` che prima era accettato; test mirati della rotazione Tyson in `overlaybatch` — PASS.
- Smoke: `batch-submit -dry-run` legge 15 job senza inviarli; `batch-render-presets -dry-run` compila tutti i 5 piani in directory temporanea rimossa alla fine.
- Nessun test di render GPU/Chronon eseguito; non necessario per questo cleanup di file byte-identici. Nessuna asserzione di performance o equivalenza visiva.

### Stato fixture e test vidrush

La fixture richiesta nel worktree è stata ripristinata copiando esattamente il file configurato dal
producer `ChrononTemplate/tools/entities_with_text/build_entity_caption_reference_v3.py`
(hash SHA-256 `00a1e9d66eab52f5e685837290036f789bc9ff083e9e1f068da5aab45436c828`), e
il font Bricolage dal catalogo Chronon3d (SHA-256 `413e7357809ddd12fd80a96a8a396de0e401638d4acd3cb3e37532f0472ac682`).
I test `vidrush-scenario` passano con i due ritratti e le due caption reali dello
scenario; nessuna asserzione è stata saltata o indebolita. La fixture è un artefatto
`out/` non tracciato: in un checkout pulito il producer deve rigenerarla dalla sua
sorgente configurata prima di eseguire quella suite.
