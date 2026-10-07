# RenderingGen Cleanup Plan 2.0

**Obiettivo:** organizzare la libreria degli overlay per ciò che l’utente vuole creare, rendere semplice capire quali animazioni si applicano a ciascuna composizione e ridurre le liste duplicate. Questo documento pianifica il lavoro: non dichiara i refactor già eseguiti.

## Decisioni di prodotto da fissare

- [ ] Usare la gerarchia **Zona → Composizione → Layout → Stile → Animazione**.
- [ ] Tenere distinti i numeri di immagine e i numeri di didascalia: “3 immagini + 2 testi” significa 3 layer immagine e 2 blocchi testo, non un nuovo tipo di renderer.
- [ ] Interpretare “Immagini con testo 1–4” come **una composizione immagini con da 1 a 4 didascalie**. Confermare con prodotto prima di congelare i nomi pubblici.
- [ ] Definire “5+ immagini” come composizione a stack/griglia con un limite tecnico documentato; il contratto non deve creare una famiglia distinta per ogni N.
- [ ] Stabilire se Camera Roll è un movimento della camera sull’intero video/scene, un movimento su una singola immagine, o entrambi. Esporli come destinazioni distinte se entrambi sono supportati.
- [ ] Stabilire ownership: ChrononTemplate resta proprietario di motion ID e ricette; il contratto semantico resta proprietario dei dati di composizione; la UI consuma un catalogo compilato e non mantiene proprie liste di ID.

## Libreria target

Le voci sotto sono **zone e composizioni navigabili**, non famiglie di animazioni. Layout, stile e animazione vanno selezionati separatamente e riutilizzati dove compatibili.

### A. Immagini

- [ ] **Immagine singola**: 1 immagine, 0 didascalie.
- [ ] **2 immagini**: layout affiancato, sovrapposto, confronto o prima/dopo.
- [ ] **3 immagini**: triptych, sequenza o immagine principale con due secondarie.
- [ ] **4 immagini**: griglia 2×2, collage o immagine principale con tre secondarie.
- [ ] **5+ immagini**: stack/griglia con numero variabile di layer.
- [ ] Definire per ogni layout vincoli di aspect ratio, crop, margini, ordine layer, selezione attiva e comportamento per asset mancanti.
- [ ] Riutilizzare lo stesso contratto a layer per 2–5+; evitare nuovi `kind` solo per il numero di immagini se il comportamento semantico è identico.
- [ ] Definire animazioni a due livelli: animazione della composizione/stack e animazione per layer. Specificare quando i layer possono avere motion indipendenti.

### B. Immagini con testo

- [ ] **Immagine/i + 1 didascalia**.
- [ ] **Immagine/i + 2 didascalie**.
- [ ] **Immagine/i + 3 didascalie**.
- [ ] **Immagine/i + 4 didascalie**.
- [ ] Specificare se le 1–4 didascalie si riferiscono a una sola immagine o se possono accompagnare qualsiasi composizione immagini.
- [ ] Modellare didascalie come elementi testuali collegati a un’immagine o a un layer tramite ID/ancora; non duplicare l’immagine per ogni didascalia.
- [ ] Stabilire layout, ordine di entrata, collisioni, limiti testo e comportamento responsive per ciascun numero di didascalie.
- [ ] Riutilizzare le motion caption esistenti quando target e layout sono compatibili; tenere motion immagine e motion testo selezionabili separatamente.

### C. Mappe

- [ ] Esporre composizioni: mappa con pin, percorso, più tappe, callout/etichette, mappa con scheda o immagine laterale.
- [ ] Specificare il coordinamento fra camera/viewport, basemap, percorso, pin, label e attribuzione.
- [ ] Dichiarare quali motion mappa sono applicabili alla basemap e quali vengono proiettate anche su pin/label.
- [ ] Tenere i dati geospaziali e le regole di proiezione nel contratto mappa; non trattare una mappa come immagine generica se deve mantenere coordinate geografiche.

### D. Background

- [ ] Separare le sorgenti: colore/gradiente, pattern/texture, immagine, video e background astratto generato.
- [ ] Specificare se ogni background copre l’intero canvas o una regione.
- [ ] Separare motion del background da motion degli overlay in primo piano.
- [ ] Definire loop, crop/fit, durata, fallback e priorità quando background e asset sorgente video sono entrambi presenti.

### E. Metriche e date

- [ ] Creare la zona editoriale **Dati**, con sottosezioni **Metriche** e **Date/Timeline**.
- [ ] Metriche: valore, unità, label, confronto/variazione e precisione; definire quali campi sono obbligatori.
- [ ] Date: data singola, intervallo e timeline di eventi; definire ordinamento e fuso/formato locale.
- [ ] Mantenere separate le composizioni `metric_stat` e `timeline_date`; condividere solo componenti visuali che hanno semantica uguale.
- [ ] Collegare i motion catalog-driven alle rispettive destinazioni semantiche e template supportati.

### F. Camera Roll

- [ ] Definire destinazioni distinte: camera sull’intera scena/video, camera su immagine singola, camera su mappa, se effettivamente supportate.
- [ ] Catalogare movimenti: pan, push/pull, zoom, tilt/roll e parallax, specificando durata, direzione, crop bounds e posa iniziale/finale.
- [ ] Definire precedenza e composizione quando un clip ha già motion camera o quando un layer possiede una propria animazione.
- [ ] Esporre Camera Roll come controllo della camera/source, non come una motion immagine, salvo il caso in cui il target sia davvero il layer immagine.

## Source of truth e contratto dati

- [ ] Mantenere **una sola fonte canonica per ogni tipo di dato**: ChrononTemplate per definizioni e ID delle motion; contratto overlay per gli elementi semantici; catalogo UI compilato per la navigazione e la compatibilità.
- [ ] Aggiungere metadati di applicabilità alle motion nel catalogo canonico: zona, target (`image_layer`, `image_stack`, `caption`, `map_view`, `background`, `camera`, `metric`, `date`), composizioni supportate, requisiti renderer e stato di certificazione.
- [ ] Generare l’inventario consumato da RenderingGen/UI a partire dal catalogo canonico. Non mantenere array paralleli di ID in Go, frontend, gallery e documentazione.
- [ ] Tenere separati i concetti `kind`, composizione, layout, preset/stile e `motion_id`. Non codificare `2_images`, `3_images`, `image_with_two_captions` come motion family.
- [ ] Definire una matrice di compatibilità generata: **composizione × target motion × renderer capability**. Le combinazioni non supportate devono risultare indisponibili già in selezione/validazione.
- [ ] Aggiungere schema e validazione per i nuovi campi senza rendere i piani legacy ambigui; versione il contratto quando cambia la semantica, non per ogni nuova motion.
- [ ] Documentare i conteggi come conteggi distinti (ID canonici) e conteggi di viste/famiglie (potenzialmente sovrapposti).

## Refactor da implementare, in ordine

### Fase 0 — Inventario e congelamento del modello attuale

- [ ] Esportare l’elenco corrente di `kind`, template, preset, categorie, motion ID, renderer capability e test/certificazioni in un report generato.
- [ ] Mappare l’uso attuale: `image`/`entity_image`, `image_layers`, `entity_card`, `map`, `metric_stat`, `timeline_date`, background e controlli camera.
- [ ] Per ogni campo o categoria segnare: owner, consumer, compatibilità legacy e fonte canonica.
- [ ] Rilevare categorie duplicate/sovrapposte e distinguerle tra alias intenzionali, selezioni UI e capacità tecniche.
- [ ] Non cancellare ID o campi in questa fase.

### Fase 1 — Disegno delle zone e della matrice di compatibilità

- [ ] Approvare le sei zone di questo piano: Immagini, Immagini con testo, Mappe, Background, Dati, Camera Roll.
- [ ] Approvare le composizioni e chiarire il significato di “testo 1–4” e “5+ immagini”.
- [ ] Compilare la matrice che associa ogni composizione ai target e alle motion consentite.
- [ ] Decidere quali categorie attuali sono raggruppamenti editoriali, quali sono compatibilità runtime e quali sono raccolte storiche/certificazione.
- [ ] Pubblicare un glossario breve: zona ≠ `kind` ≠ template ≠ preset ≠ motion.

### Fase 2 — Catalogo e selettore

- [ ] Definire lo schema del catalogo UI generato (zone, composizioni, layout, stili, target e riferimenti alle motion canoniche).
- [ ] Popolare il catalogo usando metadata e selezioni della fonte canonica; non copiare manualmente gli ID.
- [ ] Implementare la selezione in ordine: zona → composizione → layout → stile → motion compatibile.
- [ ] Consentire filtri/ricerca globale senza trasformare le categorie di catalogo in famiglie tecniche.
- [ ] Mostrare descrizione, preview, requisiti e stato di certificazione della motion; non presentare come certificate motion solo registrate.
- [ ] Rendere espliciti fallback e casi non compatibili; non scegliere silenziosamente una motion diversa.

### Fase 3 — Contratto semantico e compilazione

- [ ] Verificare se gli attuali `image_layers` supportano in modo uniforme 1, 2, 3, 4 e N immagini; fissare schema, limiti e semantica dell’ordine.
- [ ] Aggiungere riferimenti espliciti fra caption e immagine/layer e validare riferimenti orfani o duplicati.
- [ ] Definire target di animazione esplicito per composizione, singolo layer, testo/caption, viewport mappa, background e camera.
- [ ] Riutilizzare il compiler semantico come entry point unico; delegare le nuove regole ai lowering domain-specific senza introdurre un secondo renderer/compiler.
- [ ] Validare prima del render: target ammesso, asset dichiarati, motion esistente, requisiti camera/3D, proprietà supportate da Chronon e bounds temporali.
- [ ] Rendere errori e diagnostica leggibili: indicare zona/composizione/item/motion che non combacia.
- [ ] Aggiornare schema JSON, tipi Go e parity checks nello stesso cambiamento.

### Fase 4 — Motion: aggiunte, rimozioni e compatibilità

- [ ] Aggiunta a famiglia esistente: modificare la fonte canonica, sincronizzare l’artefatto incorporato, aggiornare applicability metadata e aggiungere preview/certificazione richiesta.
- [ ] Nuova capacità: prima verificare se Chronon supporta primitive e proprietà necessarie; implementare e certificare il supporto renderer prima di esporre l’ID.
- [ ] Rimozione: marcare prima l’ID come `deprecated`/non selezionabile, individuare i piani/manifests che lo usano, poi rimuoverlo solo dopo la finestra di compatibilità concordata.
- [ ] Fornire errore deterministico per ID ritirati nei piani storici; non sostituirli automaticamente con un’animazione “simile”.
- [ ] Aggiornare le selezioni batch e le gallery affinché leggano gli inventari dal catalogo anziché avere liste private.
- [ ] Conservare alias solo quando c’è un consumer identificato e una data/policy di rimozione.

### Fase 5 — Riorganizzazione del codice

- [ ] Mantenere `internal/overlay` come facciata/entry point del compiler semantico.
- [ ] Valutare estrazioni per responsabilità già separate: immagini e stack, mappe, background, camera e metriche/date; le estrazioni devono avere API piccole e non cicliche.
- [ ] Spostare harness GPU/runtime e generatori di corpus dai package di produzione in package/command dedicati, preservando entry point chiamati da CI e runbook.
- [ ] Mantenere il package `motion` responsabile di caricamento/risoluzione catalogo; evitare che `overlay` possieda seconde liste di motion.
- [ ] Unificare i report/gallery per composizione solo dopo avere confermato che usano lo stesso schema e gli stessi gate; non fondere submission, benchmark e corpus solo perché si chiamano batch.
- [ ] Rimuovere i wrapper/API legacy solo dopo ricerca dei call-site, consumer esterni e invocazioni riflessive; aggiornare il ratchet/conformance nello stesso cambiamento.

### Fase 6 — Rimozioni e pulizia

- [ ] Rimuovere dalle UI le famiglie duplicate che rappresentano solo una vista sovrapposta; mantenere le API legacy finché esistono consumer.
- [ ] Rimuovere test e gallery one-off soltanto dopo avere trasferito i casi coperti in fixture/manifests riproducibili.
- [ ] Spostare video/log di prova fuori da Git solo con report, hash, procedura di recupero e riferimenti CI/runbook verificati.
- [ ] Eliminare schema/campi obsoleti solo dopo deprecazione/versioning e prova che nessun produttore li invia.
- [ ] Non eliminare una motion solo perché non compare in una gallery o in un vecchio report di certificazione: verificare prima catalogo, piani, consumer e copertura runtime.

## Matrice di difficoltà

| Intervento | Difficoltà | Perché |
|---|---|---|
| Aggiungere una motion a un target e una categoria già supportati | Bassa–media | È data-driven se usa proprietà Chronon esistenti; restano sincronizzazione, validazione e certificazione. |
| Aggiungere un layout 2–5+ con layer già supportati | Media | Richiede regole semantiche/UI e casi di composizione, senza necessariamente cambiare renderer. |
| Aggiungere caption collegate ai layer | Media | Serve modellare riferimenti, collisioni, timing e animazioni indipendenti. |
| Aggiungere famiglia mappa/background/camera con nuova primitiva | Alta | Coinvolge contratto, lowering e capability/renderer, oltre alla certificazione. |
| Deprecare una motion senza consumer noti | Media | Serve audit degli ID nei piani e rimozione dai selettori/corpus. |
| Rimuovere un ID già pubblicato o salvato in piani | Alta | È un cambiamento di compatibilità; richiede periodo di deprecazione o migrazione esplicita. |

## Criteri di completamento

- [ ] Ogni opzione visibile nell’interfaccia ha zona, composizione, target, compatibilità e owner documentati.
- [ ] Ogni motion selezionabile arriva dalla fonte canonica e ha un target compatibile; nessuna lista manuale duplicata per ID.
- [ ] Un piano per ogni composizione Immagini 1, 2, 3, 4 e 5+ compila con conteggi layer e ordine verificati.
- [ ] Piani Immagini con 1–4 didascalie verificano associazioni e assenza di sovrapposizioni secondo le regole approvate.
- [ ] Mappe, Background, Metriche/Date e Camera Roll hanno esempi validi, invalidi e preview/certificazione coerenti con i rispettivi target.
- [ ] Un ID inesistente o incompatibile fallisce in validazione prima del render con diagnostica utile.
- [ ] Aggiungere una motion a una famiglia esistente richiede una sola modifica canonica più sync e gate; non una modifica coordinata di N liste Go/UI.
- [ ] La rimozione/deprecazione di una motion ha una procedura riproducibile e non rompe silenziosamente piani salvati.
- [ ] Le verifiche di schema/parity, compilazione semantica, conformance e render selezionati passano; riportare separatamente eventuali blocchi dovuti a fixture o hardware mancanti.

## Ordine consigliato di consegna

1. Inventario + glossario + decisioni sui conteggi e sul significato di testo 1–4.
2. Catalogo UI generato e matrice di compatibilità; nessun cambio distruttivo ai contratti.
3. Sezione Immagini (1, 2, 3, 4, 5+) riusando layer e motion esistenti.
4. Immagini con didascalie collegate e motion testo/immagine indipendenti.
5. Mappe, Background e Dati, ciascuno con target e gate propri.
6. Camera Roll dopo la decisione sulla semantica della camera e sul rapporto con il source video.
7. Deprecazioni, rimozione delle duplicazioni e pulizia degli artefatti dopo aver migrato tutti i consumer.

---

## Stato di esecuzione repository (verifica 6 ottobre 2026)

Questa sezione trasforma il piano in una checklist verificabile. **PASS** significa
che esiste codice/test o evidenza leggibile nel checkout; **PARZIALE** indica che
la capacità c’è ma manca almeno un criterio; **BLOCCATO** richiede decisione
prodotto/owner, UI assente o infrastruttura non disponibile. Non equivale a
certificazione ottica né a sign-off esterno. I cambiamenti locali già presenti
nel worktree sono preservati: questo stato non attribuisce a questo ciclo file
modificati o rimossi da un altro processo.

### Todo esecutivo e stato dei criteri

| ID | Azione / criterio verificabile | Stato | Evidenza o condizione di chiusura |
|---|---|---|---|
| T01 | Congelare il perimetro: nessun reset, checkout, submit, render, deploy o cleanup distruttivo non approvato. | [x] PASS | Worktree iniziale già sporco; nessun comando distruttivo o `sudo` usato. |
| T02 | Fissare la tassonomia editoriale senza confonderla con `kind`, template, preset e motion. | [x] PASS (inventario) | `README.md` documenta 7 sezioni principali + “Altri elementi”; questo piano ne propone 6 zone. La differenza è registrata, non risolta senza approvazione. |
| T03 | Congelare decisioni: 1–4 caption, caption globali/composite, limite 5+, semantica Camera Roll, ownership e naming pubblico. | [ ] BLOCCATO | Richiede approvazione prodotto/owner; non dedurre una policy dalla capacità corrente. |
| T04 | Inventariare canonical catalog, kind/template/preset, motion e status di certificazione distinguendo ID unici, categorie sovrapposte e report storici. | [x] PASS (snapshot) | Registri JSON embedded e test/inventari in `internal/motion` e `internal/overlay`; README separa i 388 ID dagli esiti runtime. I report `latest` sono storici (2 ottobre 2026), non certificazione completa corrente. |
| T05 | Fornire un catalogo runtime canonico con target/famiglia/capability e casi d’uso derivati, senza copiare manualmente motion ID. | [ ] PARZIALE | `runtimeMotionCatalog`/`runtimeAnimationUseCases` e test sono presenti nel worktree e i test catalogo pertinenti passano. La superficie resta package-internal (nessun consumer esterno/UI) e i target legacy non dichiarati sono esplicitati come tali; non soddisfa ancora il catalogo selettore/UI. |
| T06 | Verificare immagine singola e composizioni 2, 3, 4, 5+ riusando `image_layers`; fissare esplicitamente limiti/capacità. | [x] PASS fino a 5 / [ ] PARZIALE per 5+ | `TestCompositeEntityImageLayerCountsTwoThroughFiveCompile` verifica 2–5 e il lowering mantiene timing/motion per figlio (`overlay_v3_catalog_test.go`). Schema `image_layers` non impone `maxItems`; 5+ è tecnicamente aperto, ma non c’è limite/costo massimo approvato né fixture esplicita >5. |
| T07 | Compilare 1–4 caption associate e dimostrare collisioni/layout sul composito completo. | [ ] PARZIALE | Caption singola per entity o figlio composito, lifetime e associazione `EntityCaptionForImageID` sono presenti; test 2–5 crea una caption per layer. Non esiste una suite che asserisca la matrice caption 1–4, collisioni fra caption di layer diversi, o regole responsive approvate. |
| T08 | Validare image/caption motion per target e fallire prima del render se non compatibile. | [x] PASS (percorso caption) | `entityCaptionMotionAllowed` consulta target canonici o i legacy text-animator; lowering standalone e composito rifiuta `image_slide_left` con errore esplicito; copertura degli stili esistenti in `entity_card_layout_test.go`. Motion policy e relativo schema restano soggetti al gate stabile. |
| T09 | Collegare policy animazione group/subgroup, controllando target, duplicate selector, precedenza e override espliciti. | [x] PASS (compiler) | `animation_policy.go` applica regole gruppo/sottogruppo a frasi, immagini, caption e stack; rifiuta target/motion incompatibili e selector duplicati. `animation_policy_test.go` copre precedenza subgroup, override espliciti, target caption/image indipendenti e input invalidi; test overlay completo passa. |
| T10 | Bloccare più controller camera scena concorrenti e rendere il risultato indipendente dall’ordine item. | [x] PASS (compile-time) | `TestSceneCameraRejectsMultipleControllers` copre mappa→entità, entità→mappa e due entità; diagnosi identifica i due item. Non equivale a una decisione di prodotto su Camera Roll. |
| T11 | Verificare mappe georeferenziate: raster locale/provenienza, pin/etichette/attribuzione, LOD/viewport e camera fly-to; casi invalidi fail-closed. | [x] PASS (compilazione) | `semantic_map_test.go` copre layer grounded, attributo, pin fuori finestra, LOD/coverage/licenza, fly-to e camera nativa. Preview/render GPU correnti non risultano certificati da questi test. |
| T12 | Verificare background, metriche/date e un esempio valido/invalid per i target semantici. | [x] PASS (compilazione) | `background_contract_test.go` verifica sorgenti fit e fail-closed; `presentation_motion_wiring_test.go` compila i 50 motion Metric/Date/Entity. Certificazione hardware va riportata separatamente. |
| T13 | Stabilire separazione camera scena/clip, camera immagine e camera mappa, con precedenza/fallback approvati. | [ ] BLOCCATO | Il runtime ha fly-to mappa e cinque stili entity camera; non esiste UI Camera Roll né policy owner per source-video camera o composizione fra controller. |
| T14 | Verificare JSON Schema ↔ struct Go, campi chiusi, decode strict e compatibilità dei nuovi campi. | [x] PASS (snapshot finale verificato) | Il run finale `TestContractSchemaMatchesCompilerStructs` + `TestContractSchemaRejectsUnknownCompilerFields` passa; i campi policy, group/subgroup e caption hanno parity struct/schema e il compiler resta `DisallowUnknownFields`. |
| T15 | Inventariare tutti i 128 path MP4 baseline, preservare decisione/hash/probe e verificare i media rimasti. | [x] PASS (checkout attuale) | CSV: 128 righe, 97 presenti, 30 `removed_duplicate`, 1 `removed_in_worktree`; tutti i 97 hash/byte coincidono col CSV e tutti hanno stream H.264 con durata positiva via `ffprobe`. La verifica SHA dei blob Git originari prova per 30/30 una copia superstite con hash uguale. Il partial rimosso è vuoto. |
| T16 | Provare che i 30 path rimossi non sono più necessari e distinguere delete nel worktree da azione di questo ciclo. | [ ] PARZIALE | Nessun riferimento testuale ai 31 path rimosse trovato fuori dai file di inventario/cleanup; hash duplicati provati. La rimozione era già nel worktree, non è stata eseguita da questo ciclo; claim/report esterno e ownership/retention restano da convalidare prima di attribuire o ampliare la cancellazione. |
| T17 | Evitare eliminazioni ulteriori e ottenere sign-off retention/archive per le prove rimanenti. | [ ] BLOCCATO | Richiede owner e percorso di archive verificabile. |
| T18 | Implementare selettore UI: zona → composizione → layout → stile → motion compatibile, preview/requisiti/stato certificazione. | [ ] BLOCCATO | Nessun file UI (`tsx/jsx/vue/svelte`) nel repository RenderingGen; il componente UI va implementato nel repository proprietario. Il catalogo worker da solo non soddisfa questa accettazione. |
| T19 | Dimostrare conformance, gate unitari, standalone build, vet, scenario e render/certificazione applicabile. | [x] PASS per gate repository / [ ] BLOCCATO per certificazione GPU corrente | Sullo snapshot finale: `make test-unit`, `make test-module-standalone`, `go vet ./...` in tutti e tre i moduli, `make test-gofmt`, `make test-architecture`, `make conformance`, `git diff --check`, test overlay completo e scenario vidrush passano. Nessun render GPU è stato eseguito; report strict image storico contiene fallimenti, quindi la certificazione GPU resta non soddisfatta. |
| T20 | Deprecare/rimuovere motion o campi solo con audit consumer, finestra compatibilità e migrazione verificabile. | [x] PASS (nessuna rimozione iniziata) | Nessun ID/campo motion rimosso per questo piano; future rimozioni restano subordinate alla procedura di Fase 4. |

### Sequenza rimanente (ordine di chiusura)

1. [x] **Stabilizzare il worktree per i gate finali**; i file compiler/schema interessati non sono cambiati durante l’ultima finestra di test. Le altre modifiche locali restano intatte.
2. [x] **Chiudere T09/T14 e gate di T19**: test policy/schema-parity, overlay completo, unit, build standalone, vet, gofmt, architecture, conformance e diff-check passano.
3. [ ] **Chiudere T05** solo con consumer/UI e output catalogo concordati; il catalogo runtime package-internal non sostituisce il selettore.
4. [ ] **Chiudere T06/T07** dopo le decisioni su limite N, caption 1–4, layout e collisioni; fino ad allora non inventare bound pubblico.
5. [ ] **Chiudere T18** nel repository UI identificato dal proprietario; questo repository non contiene l’interfaccia.
6. [ ] **Chiudere T03/T13/T17** con approvazioni prodotto/owner e retention verificata.
7. [ ] Ottenere ed eseguire un gate GPU/Chronon supportato, risolvendo esplicitamente i fallimenti strict storici senza contarli come pass.
8. [ ] Solo dopo i prerequisiti, trasformare i criteri bloccati in decisioni/versioni pubbliche e chiudere le fasi UI, deprecazione e retention.

### Verifiche di questo aggiornamento

- `go test ./internal/overlay -run 'TestContractSchemaMatchesCompilerStructs|TestContractSchemaRejectsUnknownCompilerFields|TestEntityCaptionMotionAllowsEveryEntityStyle|TestSceneCameraRejectsMultipleControllers|TestCompositeEntityImageLayerCountsTwoThroughFiveCompile' -count=1` — PASS su uno snapshot precedente al successivo flusso di modifiche.
- Test focalizzato policy/schema/caption/camera/compositi/catalogo — PASS: `go test ./internal/overlay -run 'Test(AnimationPolicies|ContractSchemaMatchesCompilerStructs|ContractSchemaRejectsUnknownCompilerFields|EntityCaptionMotionAllowsEveryEntityStyle|SceneCameraRejectsMultipleControllers|CompositeEntityImageLayerCountsTwoThroughFiveCompile|CompositeEntityImageLayersKeepIndependentTimingAndMotion|RuntimeMotionCatalog|RuntimeAnimationUseCases|CompileRuntimeMotionParameters)' -count=1`.
- `go test ./internal/overlay` — PASS; `make test-unit` — PASS per renderinggen, queue e objectstore; `make test-module-standalone` — PASS per tutti e tre i moduli.
- `go vet ./...` — PASS in renderinggen, queue e objectstore; `make test-gofmt`, `make test-architecture`, `make conformance` e `git diff --check` — PASS.
- Inventario asset: confronto CSV↔filesystem byte/hash e ffprobe H.264 con durata positiva su tutti i 97 presenti; SHA dei blob `HEAD` per 30/30 rimozioni identico a una copia superstite — PASS. Prova byte-identity, non retention/sign-off esterno.
- Nessun render GPU/Chronon eseguito da questo aggiornamento. I report esistenti sono storici e `image-vulkan-nvenc-runtime-report.json` include fallimenti; certificazione GPU corrente non dichiarata.

### Avanzamento cleanup 7 ottobre 2026

- [x] Separare nel catalogo runtime `important_phrase` da `short_important_phrase`: gli ID degli stili brevi vengono esclusi dal picker generico anche se una proiezione legacy o un futuro target li includesse.
- [x] Allineare l’elenco `image_stack` al predicato `animationIsImageStack`, che è quello usato da policy e lowering. Le ricette non supportate non vengono mostrate come selezionabili.
- [x] Consolidare l’ammissione compiler-side in `motionAdmitsTarget`, riusata da policy, picker, caption e mappa. Il gate conserva il controllo delle tracce map-safe perché tutela la corrispondenza geospaziale.
- [ ] Completare i metadati canonici per eliminare i fallback legacy rimasti in `motion_admission.go`; non inventare `map_view` sui motion senza aggiornare la fonte proprietaria.
- [x] Verificare la tranche con il test mirato policy/catalogo/caption/map e con `go test ./internal/overlay -count=1`; entrambi passano. `git diff --check` è pulito.
