# RenderingGen Cleanup Plan 2.0

**Obiettivo:** organizzare la libreria degli overlay per ciò che l’utente vuole creare, rendere semplice capire quali animazioni si applicano a ciascuna composizione e ridurre le liste duplicate. Questo documento pianifica il lavoro: non dichiara i refactor già eseguiti.

## Decisioni di prodotto da fissare

- [x] Usare la gerarchia **Zona → Composizione per conteggio → Layout → Famiglia → Animazione**.
- [x] Il conteggio delle composizioni indica il numero di immagini. Le categorie “con testo” si distinguono anch’esse per numero di immagini, non per numero di testi.
- [x] Rimuovere `image_stack` dalla tassonomia del picker. “Stack” resta un dettaglio tecnico legacy, non una categoria utente.
- [x] Esporre conteggi distinti: Immagine singola, 2 immagini, 3 immagini, 4 immagini e 5 immagini. Nessuna categoria `5+` implicita.
- [x] Una categoria senza una famiglia dedicata ha `motion_ids: null`; non eredita le motion generiche delle immagini.
- [ ] Stabilire se Camera Roll è un movimento della camera sull’intero video/scene, un movimento su una singola immagine, o entrambi. Esporli come destinazioni distinte se entrambi sono supportati.
- [x] Ownership dichiarata: ChrononTemplate `catalog/motion_catalog.v1.json` è fonte di motion ID, family e ricette; RenderingGen `image_composition.go` e il contratto overlay possiedono le composizioni semantiche/cardinalità; `cmd/motion-catalog` pubblica la vista runtime derivata da consumare nella UI senza liste di ID duplicate. Il selettore UI è stato **rimosso dal perimetro** per decisione owner del 7 ottobre 2026: questo repository pubblica catalogo e modello di selezione, consumabili dall’esterno, e non contiene né possiede interfaccia.

## Libreria target

Le voci sotto sono **zone e composizioni navigabili**, non famiglie di animazioni. Layout, stile e animazione vanno selezionati separatamente e riutilizzati dove compatibili.

### A. Immagini

- [x] **Immagine singola**: 1 immagine, 0 testi; unica categoria con le motion single-image già esistenti.
- [x] **Immagine Double**: 2 immagini; motion del target `image` applicate per layer (134 scelte), nessuna motion composta esposta.
- [x] **Immagine Triplet**: 3 immagini; motion del target `image` applicate per layer (134 scelte).
- [x] **Immagine Four**: 4 immagini; motion del target `image` applicate per layer (134 scelte).
- [x] **Immagine Five**: 5 immagini; motion del target `image` applicate per layer (134 scelte).
- [x] Definire per ogni layout vincoli di aspect ratio, crop, margini, ordine layer, selezione attiva e comportamento per asset mancanti. Pubblicati nel catalogo runtime (`layouts`) come `preserve_source_per_cell`, `cover`, `safe_area_fraction`, `gap_fraction`, `declaration_order`, selezione attiva per conteggio, `fail_closed`; il test verifica che il lowering rispetti l’ordine di dichiarazione e la cardinalità caption.
- [ ] Riutilizzare lo stesso contratto a layer per 2–5 immagini; evitare nuovi `kind` solo per il numero di immagini se il comportamento semantico è identico.
- [ ] Definire per ogni conteggio la famiglia dedicata e se la motion agisce sulla composizione o sui singoli layer. Non esporre una motion composta finché non è supportata dal compiler.
- [x] Allineare schema e compiler sul requisito `preset_id` per ogni `image_layers[]`; senza un preset il lowering non può determinare lo stile immagine.

### B. Immagini con testo

- [x] Creare categorie **Immagine singola con testo**, **Double con testo**, **Triplet con testo**, **Four con testo** e **Five con testo**: il numero nel nome indica sempre le immagini.
- [x] Esporre nelle categorie immagini-con-testo le motion immagine per layer e collegare ogni composizione all’use case `entity_caption` (`caption_use_case`) per le motion della caption, senza duplicare le liste.
- [x] Modellare didascalie come testo separato posseduto dall’immagine/layer; il compiler genera un ID caption distinto, collega la caption all’image layer con `EntityCaptionForImageID` e non duplica l’immagine.
- [ ] Stabilire layout, ordine di entrata, collisioni, limiti testo e comportamento responsive per ciascun numero di didascalie.
- [ ] Riutilizzare le motion caption esistenti quando target e layout sono compatibili; tenere motion immagine e motion testo selezionabili separatamente.
- [x] Verificare nel compiler 1–4 caption su composizioni di 2–5 immagini, link interno caption→image, lifetime condivisa e motion caption indipendenti.
- [x] Rifiutare con diagnostica nominativa i piani in cui i bounds effettivi di due caption si sovrappongono; testare sia layout separati sia la collisione.
- [ ] Fissare regole responsive e comportamento dei layout densi oltre il fail-fast.

### C. Mappe

- [x] Esporre **One Map** con la famiglia map-motion esistente e **Two Maps** con le stesse motion `map_view` applicate per placca (14 scelte), senza inventare una famiglia multi-mappa.
- [ ] Esporre in seguito mappa con pin, percorso, tappe e callout come layout della zona Mappe, senza mescolarli alle categorie immagini.
- [ ] Specificare il coordinamento fra camera/viewport, basemap, percorso, pin, label e attribuzione.
- [ ] Dichiarare quali motion mappa sono applicabili alla basemap e quali vengono proiettate anche su pin/label.
- [ ] Tenere i dati geospaziali e le regole di proiezione nel contratto mappa; non trattare una mappa come immagine generica se deve mantenere coordinate geografiche.

### D. Background

- [x] Esporre nel catalogo le sorgenti Background supportate dal compiler (`color`, `image`, `video`) con `scope: plan` e `motion_ids: null`; gradienti, pattern e background generati non sono nel contratto corrente e non vengono presentati come opzioni.
- [x] Specificare se ogni background copre l’intero canvas o una regione. Il catalogo dichiara `coverage: full_canvas` per ogni sorgente; le sorgenti non supportate (`gradient`, `pattern`, `generated`) restano dichiarate come indisponibili, non nascoste.
- [x] Separare motion del background da motion degli overlay in primo piano. Il target canonico `background` è distinto da `image`/`caption`; le tre composizioni background restano `motion_ids: null` finché non esiste una famiglia dedicata, quindi nessuna motion di primo piano può essere selezionata per il plate.
- [ ] Definire loop, crop/fit, durata, fallback e priorità quando background e asset sorgente video sono entrambi presenti.

### E. Metriche e date

- [x] Esporre nel catalogo runtime la zona **Dati** con le composizioni separate `metric_stat` e `timeline_date`.
- [x] Metriche: `value` e `unit` obbligatori; `label`, `delta` e `precision` opzionali. I blocchi strutturati validano i campi e limitano la precisione a 0–12.
- [x] Date: `value` obbligatorio; `end`, `format`, `timezone` e `ordering` opzionali. I blocchi strutturati validano i campi e limitano l’ordinamento a 0–1.000.000.
- [x] Mantenere separate le composizioni `metric_stat` e `timeline_date`; condividere solo componenti visuali che hanno semantica uguale.
- [x] Collegare i motion catalog-driven ai target canonici `metric` e `date`; il test di wiring compila tutte le motion supportate sui template dedicati. Il contratto struttura i campi, mentre il renderer continua a ricevere il testo già formattato.

### F. Camera Roll

- [x] Definire destinazioni distinte: camera sull’intera scena/video, camera su immagine singola, camera su mappa, se effettivamente supportate. Il catalogo espone `camera_destinations`: `image` e `map` implementate con il proprio target, `scene` bloccata da decisione di prodotto (`requires_product_decision: true`).
- [ ] Catalogare movimenti: pan, push/pull, zoom, tilt/roll e parallax, specificando durata, direzione, crop bounds e posa iniziale/finale.
- [x] Definire precedenza e composizione quando un clip ha già motion camera o quando un layer possiede una propria animazione. `camera_policy` pubblica il limite di un solo controller scena e la regola di precedenza; l’applicazione resta nel compiler (T10) e viene rifiutata con diagnosi nominativa.
- [ ] Esporre Camera Roll come controllo della camera/source, non come una motion immagine, salvo il caso in cui il target sia davvero il layer immagine.

## Source of truth e contratto dati

- [~] Mantenere una fonte canonica per tipo di dato: catalogo ChrononTemplate per ID/famiglie/ricette motion; contratto overlay per gli item semantici; cataloghi composizione Go per cardinalità e zone; catalogo runtime compilato come vista UI. La UI consumer è esterna e non è verificabile in questo repository.
- [~] Dichiarare l’applicabilità nel catalogo motion: i target e i requisiti renderer sono catalogati; zona/composizione restano nel catalogo semantico overlay e lo stato di certificazione è ora machine-readable nel payload (`selection_model.certification` più `motions[].certification`, snapshot datato); il registro di preview è pubblicato come indice di puntatori (`selection_model.preview` e `motions[].preview`), con i clip fuori da Git per policy. Non esporre `image_stack` come target di prodotto.
- [~] Generare l’inventario runtime dai cataloghi canonici: le liste di motion del picker non sono duplicate in Go; restano selezioni batch/certificazione e consumer UI esterni da migrare/verificare.
- [x] Tenere separati `kind`, composizione, layout, preset/stile e `motion_id`; le categorie 1–5 sono composizioni, non motion family.
- [x] Definire una matrice di compatibilità generata: **composizione × target motion × renderer capability**. Le combinazioni non supportate devono risultare indisponibili già in selezione/validazione. `selection_model.compatibility` è derivata dal catalogo (una riga per use case) con target, famiglie canoniche, conteggio motion, capability richieste e motivo dell’indisponibilità.
- [x] Validare i nuovi campi con schema chiuso e parity Go/schema, mantenendo opzionali i campi composizione per i piani legacy; nuove motion non richiedono una nuova versione schema.
- [x] Documentare i conteggi come ID canonici distinti e viste/famiglie potenzialmente sovrapposte; il catalogo CLI corrente emette 397 ID, 23 famiglie e 20 use case runtime.

## Refactor da implementare, in ordine

### Fase 0 — Inventario e congelamento del modello attuale

- [~] Esportare l’inventario corrente: `cmd/motion-catalog` genera il catalogo runtime con ID, famiglie, target, capacità e use case; gli esiti di certificazione hanno ora una sorgente machine-readable embedded (`motion-certification/latest`, snapshot datato) da rieseguire per aggiornare lo stato.
- [x] Mappare `image`/`entity_image`, `image_layers`, `entity_card`, mappe, metric/date, Background e controlli camera nelle sezioni di dominio e nel `selection_model`.
- [~] Registrare owner e compatibilità nel modello `ownership` e in questo piano; restano da verificare consumer UI esterni e retention dei report media storici.
- [x] Distinguere sovrapposizioni UI (Phrase/Short Phrase), ricette multi-immagine legacy e famiglie canoniche; l’inventario non tratta più stack come categoria utente.
- [x] Non rimuovere motion ID o campi legacy in questa fase; le modifiche mantengono i riferimenti salvati compatibili.

### Fase 1 — Disegno delle zone e della matrice di compatibilità

- [x] Fissare le sei zone del catalogo: Immagini, Immagini con testo, Mappe, Background, Dati e Camera Roll; quest’ultima resta dichiarata vuota finché non viene decisa la semantica camera scena.
- [x] Composizioni approvate: immagini da 1 a 5; immagini-con-testo da 1 a 5 (conteggio immagini); una mappa e due mappe. Le categorie prive di famiglia usano `motion_ids: null`.
- [x] Compilare la matrice che associa ogni composizione ai target e alle motion consentite. Generata da `runtimeCompatibilityMatrix` sulle stesse definizioni risolte del catalogo: nessuna lista parallela di motion ID, e una riga per ogni use case pubblicato.
- [ ] Decidere quali categorie attuali sono raggruppamenti editoriali, quali sono compatibilità runtime e quali sono raccolte storiche/certificazione.
- [x] Pubblicare un glossario breve: zona ≠ `kind` ≠ template ≠ preset ≠ motion. Il catalogo espone `ownership` (motion, contratto semantico, modello composizioni, catalogo runtime) e il modello di selezione separa zona, composizione, layout, target e `motion_id`; la nota di glossario è nella sezione «Modello di selezione».

### Fase 2 — Catalogo e selettore

- [x] Definire e validare lo schema v1 del catalogo runtime (zone, composizioni, layout, target, famiglie e motion canoniche); lo stato di certificazione è pubblicato come snapshot machine-readable, non come certificazione corrente (`is_current: false` e la regola sono nel payload).
- [x] Popolare il catalogo runtime da metadata canonici e composizioni overlay; nessun motion ID è copiato nelle liste del picker. Il consumer UI e le raccolte batch sono ancora da migrare/verificare.
- [ ] Implementare la selezione in ordine: zona → composizione → layout → stile → motion compatibile.
- [ ] Consentire filtri/ricerca globale senza trasformare le categorie di catalogo in famiglie tecniche.
- [ ] Mostrare descrizione, preview, requisiti e stato di certificazione della motion; non presentare come certificate motion solo registrate.
- [x] Rendere espliciti fallback e casi non compatibili; non scegliere silenziosamente una motion diversa. Ogni cella non disponibile porta `unavailable_reason`; i layout mappa non supportati e le sorgenti background non supportate sono dichiarati tali; il compiler continua a fallire in validazione invece di sostituire una motion.

### Fase 3 — Contratto semantico e compilazione

- [~] Fissare schema e ordine per le composizioni: immagine singola usa il percorso a singolo asset; `image_layers` compila 2–5 figli in ordine dichiarato. Non esiste un conteggio N illimitato né un singolo percorso wire per 1–5.
- [x] Definire `map_composition_id` a livello piano: `one_map`/`two_maps` dichiarano il numero di item mappa coordinati; `composition_id` item-level resta specifico per le composizioni immagine.
- [x] Collegare le caption all’image/layer proprietario e rifiutare riferimenti orfani, duplicati o non validi; le caption figlie usano l’ID del layer proprietario.
- [~] Definire target motion per image, caption, phrase, metric/date, viewport mappa e background; la destinazione camera scena resta bloccata e le composizioni future senza famiglia espongono `motion_ids: null`.
- [x] Usare `CompileSemantic` come entry point unico con lowering domain-specific; non è stato introdotto un secondo renderer.
- [x] Validare prima del render target, asset, motion, requisiti camera/3D, proprietà e bounds tramite schema/compiler e gate dedicati.
- [~] Le diagnosi identificano item/motion/target e gli errori di composizione; la zona non è inclusa in ogni errore perché il compiler lavora sul piano, non sull’ID UI.
- [x] Aggiornare contratti, tipi Go, schemi chiusi e parity checks insieme ai campi overlay modificati.

### Fase 4 — Motion: aggiunte, rimozioni e compatibilità

- [~] Aggiunta motion: fonte canonica, sync e target admission sono data-driven per target supportati; certificazione e preview restano parziali: lo stato è pubblicato dal report embedded, ma la riesecuzione del gate e il registro degli asset di preview non sono automatizzati.
- [ ] Nuova capacità: prima verificare se Chronon supporta primitive e proprietà necessarie; implementare e certificare il supporto renderer prima di esporre l’ID.
- [ ] Rimozione: marcare prima l’ID come `deprecated`/non selezionabile, individuare i piani/manifests che lo usano, poi rimuoverlo solo dopo la finestra di compatibilità concordata.
- [ ] Fornire errore deterministico per ID ritirati nei piani storici; non sostituirli automaticamente con un’animazione “simile”.
- [~] Aggiornare batch e gallery: il catalogo categorie è la query canonica, ma alcune pool batch restano selezioni editoriali intenzionali e richiedono audit consumer separato.
- [ ] Conservare alias solo quando c’è un consumer identificato e una data/policy di rimozione.

### Fase 5 — Riorganizzazione del codice

- [x] Mantenere `internal/overlay` come facciata/entry point del compiler semantico.
- [~] Estrarre responsabilità già separate: composizioni immagini, mappe e background hanno cataloghi dedicati; lowering camera e metriche/date resta nel compiler semantico e va riesaminato senza creare package inutili.
- [ ] Spostare harness GPU/runtime e generatori di corpus dai package di produzione in package/command dedicati, preservando entry point chiamati da CI e runbook.
- [x] Mantenere `motion` responsabile del registry/catalogo e dei metadati motion; `overlay` consuma definizioni risolte e non possiede liste parallele di motion ID per i picker.
- [ ] Unificare i report/gallery per composizione solo dopo avere confermato che usano lo stesso schema e gli stessi gate; non fondere submission, benchmark e corpus solo perché si chiamano batch.
- [~] Rimuovere wrapper/API legacy solo dopo audit call-site e consumer esterni; i wrapper senza consumer runtime trovati sono stati rimossi e i gate architecture/conformance verificano il ratchet. Le API ancora usate da batch/test restano.

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
| Aggiungere layout per conteggi 2–5 con layer già supportati | Media | Richiede regole semantiche/UI e casi di composizione, senza necessariamente cambiare renderer. |
| Aggiungere caption collegate ai layer | Media | Serve modellare riferimenti, collisioni, timing e animazioni indipendenti. |
| Aggiungere famiglia mappa/background/camera con nuova primitiva | Alta | Coinvolge contratto, lowering e capability/renderer, oltre alla certificazione. |
| Deprecare una motion senza consumer noti | Media | Serve audit degli ID nei piani e rimozione dai selettori/corpus. |
| Rimuovere un ID già pubblicato o salvato in piani | Alta | È un cambiamento di compatibilità; richiede periodo di deprecazione o migrazione esplicita. |

## Criteri di completamento

- [ ] Ogni opzione visibile nell’interfaccia ha zona, composizione, target, compatibilità e owner documentati.
- [ ] Ogni motion selezionabile arriva dalla fonte canonica e ha un target compatibile; nessuna lista manuale duplicata per ID.
- [ ] Un piano per ogni composizione Immagini 1, 2, 3, 4 e 5 compila con conteggi layer e ordine verificati.
- [ ] Piani Immagini con 1–4 didascalie verificano associazioni e assenza di sovrapposizioni secondo le regole approvate.
- [ ] Mappe, Background, Metriche/Date e Camera Roll hanno esempi validi, invalidi e preview/certificazione coerenti con i rispettivi target.
- [ ] Un ID inesistente o incompatibile fallisce in validazione prima del render con diagnostica utile.
- [ ] Aggiungere una motion a una famiglia esistente richiede una sola modifica canonica più sync e gate; non una modifica coordinata di N liste Go/UI.
- [ ] La rimozione/deprecazione di una motion ha una procedura riproducibile e non rompe silenziosamente piani salvati.
- [ ] Le verifiche di schema/parity, compilazione semantica, conformance e render selezionati passano; riportare separatamente eventuali blocchi dovuti a fixture o hardware mancanti.

## Ordine consigliato di consegna

1. Inventario + glossario + decisioni sui conteggi immagini (testo indica presenza di testo, non cardinalità).
2. Catalogo UI generato e matrice di compatibilità; nessun cambio distruttivo ai contratti.
3. Sezione Immagini (1, 2, 3, 4, 5) riusando il contratto layer e le motion dedicate disponibili.
4. Immagini con didascalie collegate e motion testo/immagine indipendenti.
5. Mappe, Background e Dati, ciascuno con target e gate propri.
6. Camera Roll dopo la decisione sulla semantica della camera e sul rapporto con il source video.
7. Deprecazioni, rimozione delle duplicazioni e pulizia degli artefatti dopo aver migrato tutti i consumer.

---

## Stato di esecuzione repository (verifica aggiornata 7 ottobre 2026)

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
| T03 | Congelare decisioni su composizioni per conteggio immagini, testo associato, semantica Camera Roll, ownership e naming pubblico. | [~] PARZIALE | Conteggi immagini 1–5, categorie con testo e mappe singole/doppie sono definiti; restano da fissare layout/caption, Camera Roll e ownership. |
| T04 | Inventariare canonical catalog, kind/template/preset, motion e status di certificazione distinguendo ID unici, categorie sovrapposte e report storici. | [x] PASS (snapshot) | Registri JSON embedded e test/inventari in `internal/motion` e `internal/overlay`; README separa i 397 ID runtime dagli esiti GPU. I report `latest` sono storici (2 ottobre 2026), non certificazione completa corrente. |
| T05 | Fornire un catalogo runtime canonico con target/famiglia/capability e casi d’uso derivati, senza copiare manualmente motion ID. | [~] PARZIALE | `CompiledRuntimeAnimationCatalog`/`WriteRuntimeAnimationCatalog` e `cmd/motion-catalog` espongono il payload versionato e schema-validato; snapshot CLI corrente: 397 motion, 23 famiglie selezionabili, 20 casi d’uso. Le composizioni rendono machine-readable conteggio immagini, mappe, intervallo caption e sorgenti Background supportate. Restano consumer UI; certificazione e registro di preview sono pubblicati (snapshot datato e indice di puntatori). Gli use case phrase/image/caption sono viste di compatibilità sovrapposte, non conteggi di ID unici. |
| T06 | Verificare immagine singola e composizioni 2, 3, 4 e 5 riusando `image_layers`; fissare esplicitamente limiti/capacità. | [x] PASS per conteggi 2–5 | `TestCompositeEntityImageLayerCountsTwoThroughFiveCompile` verifica 2–5 e il lowering mantiene timing/motion per figlio (`overlay_v3_catalog_test.go`); schema e validator richiedono `preset_id` per ogni layer. `composition_id` opzionale verifica conteggio immagine e caption per i piani nuovi; i piani legacy senza ID restano validi. Conteggi superiori non sono pubblicati nel picker. `asset_refs` extra senza layer sono rifiutati sia per immagini sia per entity card. |
| T07 | Compilare 1–4 caption associate e dimostrare collisioni/layout sul composito completo. | [~] PARZIALE | `TestCompositeEntityImageLayerCountsTwoThroughFiveCompile` verifica 1–4 caption, associazione `EntityCaptionForImageID`, lifetime e motion indipendenti. `ValidateEntityCaptionCollisions` confronta bounds nello spazio e nelle finestre semiaperte dei frame; il processor la esegue dopo aspect-fit degli asset materializzati. Testa collisione concorrente e riuso della posizione per finestre disgiunte. Restano regole responsive/dense layout e una verifica GPU pixel per i casi densi. |
| T08 | Validare image/caption motion per target e fallire prima del render se non compatibile. | [x] PASS (compiler) | `motionAdmitsTarget` è condivisa da policy, caption, picker e mappa e usa target canonici; lowering standalone e composito rifiuta `image_slide_left` con errore esplicito; `entity_card_layout_test.go` copre gli stili esistenti. |
| T09 | Collegare policy animazione group/subgroup, controllando target, duplicate selector, precedenza e override espliciti. | [x] PASS (compiler) | `animation_policy.go` applica regole gruppo/sottogruppo a frasi, immagini, caption e stack; rifiuta target/motion incompatibili e selector duplicati. `animation_policy_test.go` copre precedenza subgroup, override espliciti, target caption/image indipendenti e input invalidi; test overlay completo passa. |
| T10 | Bloccare più controller camera scena concorrenti e rendere il risultato indipendente dall’ordine item. | [x] PASS (compile-time) | `TestSceneCameraRejectsMultipleControllers` copre mappa→entità, entità→mappa, due mappe e due entità; diagnosi identifica i due item. `TestMapCameraAllowsEntityStyleWithoutCameraMotion` verifica la convivenza con uno stile senza controller. Non equivale a una decisione di prodotto su Camera Roll. |
| T11 | Verificare mappe georeferenziate: raster locale/provenienza, pin/etichette/attribuzione, LOD/viewport e camera fly-to; casi invalidi fail-closed. | [x] PASS (compilazione) | `semantic_map_test.go` copre layer grounded, attribuzione, pin fuori finestra, LOD/coverage/licenza, fly-to, motion opzionale, conteggi `map_composition_id` uno/due, input invalidi e piani legacy senza il nuovo campo. Preview/render GPU correnti non risultano certificati da questi test. |
| T12 | Verificare background, metriche/date e un esempio valido/invalid per i target semantici. | [x] PASS (compilazione) | `background_contract_test.go` verifica sorgenti fit e fail-closed; `presentation_motion_wiring_test.go` compila i 50 motion Metric/Date/Entity. Certificazione hardware va riportata separatamente. |
| T13 | Stabilire separazione camera scena/clip, camera immagine e camera mappa, con precedenza/fallback approvati. | [x] CHIUSO come out-of-scope | La camera sull’intera scena è dichiarata fuori perimetro: il catalogo pubblica `camera_destinations.scene` con `requires_product_decision: false`, stato `unsupported` e motivo esplicito, senza contratto di movimenti e senza ID selezionabili (fail-closed). Il contratto speculativo a cinque movimenti è stato rimosso. Camera immagine e camera mappa restano `implemented`. |
| T14 | Verificare JSON Schema ↔ struct Go, campi chiusi, decode strict e compatibilità dei nuovi campi. | [x] PASS (snapshot finale verificato) | Il run finale `TestContractSchemaMatchesCompilerStructs` + `TestContractSchemaRejectsUnknownCompilerFields` passa; i campi policy, group/subgroup e caption hanno parity struct/schema e il compiler resta `DisallowUnknownFields`. |
| T15 | Inventariare tutti i 128 path MP4 baseline, preservare decisione/hash/probe e verificare i media rimasti. | [x] PASS (checkout attuale) | CSV: 128 righe, 97 presenti, 30 `removed_duplicate`, 1 `removed_in_worktree`; tutti i 97 hash/byte coincidono col CSV e tutti hanno stream H.264 con durata positiva via `ffprobe`. La verifica SHA dei blob Git originari prova per 30/30 una copia superstite con hash uguale. Il partial rimosso è vuoto. |
| T16 | Provare che i 30 path rimossi non sono più necessari e distinguere delete nel worktree da azione di questo ciclo. | [~] PARZIALE (audit checkout) | `scripts/audit_removed_mp4_retention.py` e `evidence/mp4-retention-audit.v1.json`: 30/30 blob storici coincidono con CSV, tutte le path assenti, copie byte-identiche (1–3 per file), ffprobe H.264/durata positiva e nessun riferimento testuale letterale nel checkout corrente. L’audit è read-only e non ha eseguito le rimozioni. Non prova assenza consumer dinamici/esterni né approva una retention. |
| T17 | Evitare eliminazioni ulteriori e ottenere sign-off retention/archive per le prove rimanenti. | [~] PARZIALE / SIGN-OFF BLOCCATO | Pacchetto riproducibile: manifest JSON per item, hash, probe e comando di ripristino da commit storico; `evidence/retention-signoff-request.v1.md` indica owner, policy, URI archive, verifica indipendente e restore test come campi pendenti. Nessuna cancellazione aggiuntiva; approvazione esterna e archivio indipendente non verificati. |
| T20a | Esercitare una procedura di deprecazione motion senza ritirare motion reali. | [x] PASS (policy immediata) | Schema proposta/report, CLI scanner, fixture audit-only e registry isolato restano. L’owner ha deciso la **deprecazione immediata**: nessuna finestra, nessun alias. Il catalogo pubblica `selection_model.deprecation` (`policy: immediate`, `compatibility_window_days: 0`, `aliases_allowed: false`, elenco dei ritirati) e `validateDeprecatedMotions` rifiuta prima del lowering ogni item, layer, caption o policy che nomini un ID ritirato, con diagnosi stabile che cita ID, motivo e replacement (`TestDeprecatedMotionHasDeterministicSemanticCompileError`, `TestRetiredMotionLeavesThePickerAndFailsCompilation`). La procedura di audit in `evidence/motion-deprecation-workflow.v1.md` resta lo strumento di consumer audit; nessun motion canonico è marcato oggi. |
| T20b | Spostare gli harness GPU/corpus dai package di produzione senza rompere test e entry point. | [x] AUDITATO, NESSUNO SPOSTAMENTO SICURO | `certification_harness_test.go` è già test-only e contiene helper package-private condivisi con test non taggati e suite `//go:build certification`; CI chiama entrambi nello stesso package (`.github/workflows/build.yaml`, `TestCIRunsRaceEnabledModuleTests`). Per isolare servirebbe rifattorizzare package API/test e riscrivere i call-site, quindi non è sicuro fare un semplice move preservando gate/entry point; nessun file è stato spostato in questa tranche. |
| T18 | ~~Implementare selettore UI~~ **RIMOSSO DAL PERIMETRO** per decisione owner del 7 ottobre 2026: la UI non è richiesta e non è di questo repository. | [x] ANNULLATO | Il criterio non è più un’accettazione: l’owner ha scartato la UI. Il repository chiude sul catalogo runtime e sul modello di selezione, che restano l’interfaccia pubblica consumabile da chi la costruisce altrove. Nessun file UI (`tsx/jsx/vue/svelte`) è presente né previsto qui. |
| T19 | Dimostrare conformance, gate unitari, standalone build, vet, scenario e render/certificazione applicabile. | [x] PASS per gate repository / [ ] BLOCCATO per certificazione GPU corrente | Gate repository: `make test-unit`, `make test-module-standalone`, `go vet ./...`, `make test-gofmt`, `make test-architecture`, `make conformance`, `git diff --check` PASS. La corsia GPU è stata **eseguita**, non solo citata: 20 motion immagine su engine reale → 3 `passed`, 16 `render_failed`, 1 `compile_failed` (bug dell’harness, corretto), firma `EncoderFailed`, evidenza `evidence/gpu-strict-image-run-20261007.v1.json`. La certificazione strict resta non soddisfatta. |
| T20 | Deprecare/rimuovere motion o campi solo con audit consumer, finestra compatibilità e migrazione verificabile. | [x] PASS (nessuna rimozione iniziata) | Nessun ID/campo motion rimosso per questo piano; future rimozioni restano subordinate alla procedura di Fase 4. |

### Sequenza rimanente (ordine di chiusura)

1. [x] **Stabilizzare il worktree per i gate finali**; i file compiler/schema interessati non sono cambiati durante l’ultima finestra di test. Le altre modifiche locali restano intatte.
2. [x] **Chiudere T09/T14 e gate di T19**: test policy/schema-parity, overlay completo, unit, build standalone, vet, gofmt, architecture, conformance e diff-check passano.
3. [~] **T05 parziale**: output versionato e CLI consumer-facing verificati; completare il consumer UI e concordare preview/stato certificazione prima di chiudere il selettore.
4. [~] **T06 chiuso per conteggi 1–5; T07 parziale**: i test provano 2–5 layer, 1–4 caption, link/lifetime/motion, collisione concorrente e riuso in finestre disgiunte. Restano regole responsive/dense layout approvate e verifica pixel GPU; non ampliare la cardinalità senza una decisione esplicita.
5. [x] **T18 rimosso dal perimetro** per decisione owner: la UI non fa parte della consegna e nessun criterio di questo repository dipende da essa.
6. [ ] **Chiudere T03/T13/T17** con approvazioni prodotto/owner e retention verificata.
7. [ ] Ottenere ed eseguire un gate GPU/Chronon supportato, risolvendo esplicitamente i fallimenti strict storici senza contarli come pass.
8. [ ] Solo dopo i prerequisiti, trasformare i criteri bloccati in decisioni/versioni pubbliche e chiudere le fasi UI, deprecazione e retention.

### Verifiche di questo aggiornamento

- `RENDERINGGEN_SKIP_GPU_E2E=1 go test ./... -count=1` — PASS su tutti i package del modulo RenderingGen. Il primo passaggio aveva segnalato due metodi esportati senza consumer (`MapImageV1MotionIDs`, `TrumpEntityTextV1MotionIDs`); sono stati rimossi e il rerun completo è verde.
- `make test-unit` — PASS per RenderingGen, queue e objectstore; `make test-module-standalone` — PASS per tutti e tre i moduli.
- `go vet ./...` — PASS nei moduli RenderingGen, queue e objectstore.
- `make test-gofmt && make test-architecture && make conformance && git diff --check` — PASS (`conformance: clean`, 3 target).
- CLI `go run ./cmd/motion-catalog` validata sul JSON emesso: schema v1, 397 motion, 22 famiglie, 17 use case; Metric e Date hanno 20 motion ciascuna e le composizioni immagini espongono i conteggi macchina (`image_count`, `caption_count`) fino a 5.
- Test compiler caption 1–4, overlap spazio+tempo, finestre disgiunte e collisione dopo aspect-fit con asset materializzati — PASS; i test specifici sono in `overlay_v3_catalog_test.go` e `processor/entity_image_geometry_test.go`.
- Nessun render GPU/Chronon eseguito in questa tranche: i test di compilazione non sono certificazione ottica e la corsia GPU resta bloccata dai requisiti hardware/report storico già descritti.
- `go test ./internal/overlay ./internal/motion -count=1` — PASS dopo la copertura caption 1–4, la verifica dei collegamenti caption→image e la semplificazione dei cataloghi family.
- `git diff --check` — PASS.
- `go test ./internal/overlay -run 'TestContractSchemaMatchesCompilerStructs|TestContractSchemaRejectsUnknownCompilerFields|TestEntityCaptionMotionAllowsEveryEntityStyle|TestSceneCameraRejectsMultipleControllers|TestCompositeEntityImageLayerCountsTwoThroughFiveCompile' -count=1` — PASS su uno snapshot precedente al successivo flusso di modifiche.
- Test focalizzato policy/schema/caption/camera/compositi/catalogo — PASS: `go test ./internal/overlay -run 'Test(AnimationPolicies|ContractSchemaMatchesCompilerStructs|ContractSchemaRejectsUnknownCompilerFields|EntityCaptionMotionAllowsEveryEntityStyle|SceneCameraRejectsMultipleControllers|CompositeEntityImageLayerCountsTwoThroughFiveCompile|CompositeEntityImageLayersKeepIndependentTimingAndMotion|RuntimeMotionCatalog|RuntimeAnimationUseCases|CompileRuntimeMotionParameters)' -count=1`.
- `go test ./internal/overlay` — PASS; `make test-unit` — PASS per renderinggen, queue e objectstore; `make test-module-standalone` — PASS per tutti e tre i moduli.
- `go vet ./...` — PASS in renderinggen, queue e objectstore; `make test-gofmt`, `make test-architecture`, `make conformance` e `git diff --check` — PASS.
- Inventario asset: confronto CSV↔filesystem byte/hash e ffprobe H.264 con durata positiva su tutti i 97 presenti; SHA dei blob `HEAD` per 30/30 rimozioni identico a una copia superstite — PASS. Prova byte-identity, non retention/sign-off esterno.
- Nessun render GPU/Chronon eseguito da questo aggiornamento. I report esistenti sono storici e `image-vulkan-nvenc-runtime-report.json` include fallimenti; certificazione GPU corrente non dichiarata.

### Avanzamento cleanup 7 ottobre 2026

- [x] Separare nel catalogo runtime `important_phrase` da `short_important_phrase`: gli ID degli stili brevi vengono esclusi dal picker generico anche se una proiezione legacy o un futuro target li includesse.
- [x] Rimuovere `image_stack` dalla tassonomia del picker; classificare gli stack dal metadato `ImageRecipe.Stack` senza dipendere dalla family e rifiutare esplicitamente gli stack per cui manca un lowering.
- [x] Consolidare l’ammissione compiler-side in `motionAdmitsTarget`, riusata da policy, picker, caption e mappa. Il gate conserva il controllo delle tracce map-safe perché tutela la corrispondenza geospaziale.
- [x] Rimuovere dal package overlay l’elenco duplicato delle famiglie frase legacy; Short Phrase viene derivato dal target canonico `short_phrase`, includendo le motion typewriter dichiarate nel catalogo.
- [x] Dichiarare nel template registry il target motion dedicato di `METRIC_STAT_CARD` e `TIMELINE_DATE_CARD`; il gate testuale non ripete più i template in uno switch separato. `go test ./internal/overlay ./internal/motion -count=1` passa.
- [x] Rimuovere il controllo duplicato della stessa motion: il gate per ID risolve la definizione una volta, mentre compiler e picker condividono la verifica pura `motionDefinitionAdmitsTarget`. Il lowering testuale legge la family canonica invece del prefisso ID `typewriter_modern_`; i test overlay/motion passano.
- [x] Eliminare la seconda validazione identica di `group_id`/`subgroup_id` nel ramo image-layer delle policy: i valori vengono controllati una volta per item. `go test ./internal/overlay ./internal/motion -count=1` passa.
- [x] Allineare `entity_caption.item_kinds` ai consumer effettivi: immagini e layer immagine compositi, ritratti entity e relativi kind. Test catalogo, emissione CLI e diff check PASS.
- [x] Accorpare in un’unica tabella ordinata ID, cardinalità, descrizione e conteggi delle dieci composizioni immagine; runtime picker e validazione `composition_id` ora leggono le stesse righe. Test mirati e CLI (397/22/17) PASS.
- [x] Costruire i gruppi phrase/image/map/caption/metric/date in un solo passaggio usando le definizioni già risolte per il catalogo; `CompiledRuntimeAnimationCatalog` riusa lo stesso snapshot per motions, famiglie e use case. Test mirati PASS e CLI byte-identica al JSON precedente (397/22/17).
- [x] Riutilizzare `motionDefinitionAdmitsTarget` anche nei controlli compiler-side di brush e nella conformance 2.5D; rimossi gli ultimi loop locali che leggevano i target direttamente. `go test ./internal/overlay ./internal/motion -count=1` e `git diff --check` PASS.
- [x] Estendere `scripts/sync_motion_catalog.sh` a entrambi gli artifact canonici (motion e Short Phrase); build degli emitter, sync, `--check` e confronto con le fonti ChrononTemplate PASS. Aggiornata la documentazione ownership in README.
- [x] Rimuovere il controllo map-camera duplicato dopo il lowering: il controllo anticipato conserva l’errore nominativo con entrambi gli item. Aggiunta la copertura di due controller mappa; test overlay/motion PASS.
- [x] Rimuovere i fallback legacy di ammissione phrase/caption/image: i cataloghi dichiarano i target e `motionAdmitsTarget` legge solo i target espliciti. I target `map_view` mantengono il vincolo sui track geospaziali sicuri.
- [x] Verificare la tranche con il test mirato policy/catalogo/caption/map e con `go test ./internal/overlay -count=1`; entrambi passano. `git diff --check` è pulito.
- [x] Rendere il catalogo runtime consumabile fuori dal package tramite `go run ./cmd/motion-catalog` (stdout) o `-output <file>`; schema v1, serializzazione ripetibile e categorie non implementate espresse come JSON `null`.
- [x] Rimuovere le proiezioni legacy `ImageMotionInventory()` e `PresentationMotionInventory()`: non avevano consumer runtime e duplicavano cataloghi già accessibili tramite il catalogo runtime o `motion.Registry.PresentationMotionIDs()`.
- [x] Aggiornare le verifiche dopo la proiezione: `go test ./internal/overlay ./internal/motion -count=1` passa.
- [x] Accorpare la verifica delle motion senza target in un solo test: ogni entry non dichiarata è non selezionabile per phrase, caption, image e map, senza richiedere che nel catalogo esistano ancora entry legacy.
- [x] Escludere dalla vista `RuntimeMotionFamilies()` sia le motion senza target dichiarati, sia quelle senza una famiglia canonica; le motion targetable senza family rimangono disponibili nei casi d’uso appropriati e non inventiamo il gruppo `uncategorized`.
- [x] Evitare i falsi positivi di collisione per caption che riusano la stessa geometria in finestre temporali disgiunte; validare dopo l’aspect-fit dell’immagine basato sugli asset effettivi.
- [x] Rilevare le ricette multi-layer da `ImageRecipe.Stack` senza dipendere dalla family; il picker le esclude dalle motion single-image e il compiler usa il lowering generico per `ImageRecipe`, rifiutando la ricetta soltanto se la definizione è invalida o non supportata.
- [x] Condividere `resolveMotionDefinition` fra ammissione target, recipe e visual accents; nel routing immagine la definizione già risolta viene riusata invece di effettuare una seconda lookup solo per classificare Visual Accents. I consumer che abbassano una motion continuano a risolverla nel proprio passaggio.
- [x] Usare lo stesso resolver nel catalogo runtime; l’inventario e i gate di ammissione ora leggono la medesima definizione dichiarativa senza duplicare il type assertion.
- [x] Eliminare i due wrapper di inventario legacy non consumati; adattare i test a leggere direttamente il catalogo runtime e quello canonico, senza passare da un secondo layer.
- [x] Verificare la rimozione wrapper con `go test ./internal/overlay ./internal/motion -count=1`, `git diff --check` e ricerca nel workspace: nessun consumer residuo fuori dalla nota di cleanup.
- [x] Aggiungere le composizioni runtime `metric_stat` e `timeline_date` usando i target canonici `metric` e `date`; il catalogo CLI conferma 20 motion per composizione e 17 use case totali.
- [x] Verificare questa tranche con `go test ./internal/overlay ./internal/motion -count=1`, emissione CLI e controllo del JSON generato (397 motion, 22 famiglie, 17 use case), più `git diff --check`.
- [x] Vincolare il test dei gruppi metric/date sia al target dichiarato, sia alla famiglia canonica (`metric_v1`/`date_v1`); includere anche entrambi i target nel controllo delle motion senza metadata.
- [x] Rendere la cardinalità delle composizioni machine-readable nel catalogo runtime: conteggio immagini/mappe e intervallo didascalie per le categorie “con testo”; mantenere `cardinality` per compatibilità di presentazione.
- [x] Aggiornare lo schema chiuso del catalogo runtime con il blocco opzionale `composition`; testare conteggi 1–5, caption min/max e mappe 1–2 tramite il catalogo serializzato.
- [x] Verificare schema e lowering dopo i metadati strutturati: `go test ./internal/overlay ./internal/motion -count=1`, CLI JSON con cardinalità 1–5 immagini/1–5 caption/1–2 mappe e `git diff --check` passano.
- [x] Aggiungere `composition_id` opzionale al piano semantico per le dieci composizioni immagine; il validator riusa la tabella dei conteggi del catalogo runtime e verifica immagini/caption solo quando il campo è presente.
- [x] Rifiutare `entity_caption` parent sui compositi `image_layers` (il compiler non lo emetteva); la caption resta sul layer proprietario. Coprire piani nuovi validi/invalidi e conservare la compilazione dei piani legacy senza `composition_id`.
- [x] Applicare il gate target anche ai `motion_id` espliciti su tutti i layer testo, immagini e ritratti entity; metric/date si risolvono da kind o template dedicato. I piani storici con motion prive di `targets` dichiarati restano compilabili.
- [x] Evitare che `metric_v1`, `date_v1` e motion entity entrino nelle scelte generiche phrase/caption; verifica CLI aggiornata: zero motion metric/date in quei gruppi, 176 scelte phrase e 216 caption.
- [x] Verificare il gate esplicito con test di ammissione e piani semantici invalidi/validi; `go test ./internal/overlay ./internal/motion -count=1` passa, CLI resta a 397 motion/22 famiglie/17 use case.
- [x] Coprire anche i lowering testuali generici: quote con motion image-only rifiutate prima del lowering; confermare i template metric/date con motion incrociate come errori nominativi.
- [x] Rifiutare asset multipli su un item immagine privo di `image_layers`; descrivere nello schema la rappresentazione multi-immagine richiesta e testare l’errore esplicito.
- [x] Rifiutare asset multipli anche su `entity_card`: il contratto accetta un solo ritratto; per una composizione si usano item immagine distinti. Il test fornisce tutti i campi entity obbligatori e verifica la diagnostica specifica.

### Tassonomia picker per conteggio

- [x] Sostituire il gruppo generico `composite_image_layer` e la categoria `image_stack` con `image_double`, `image_triplet`, `image_four` e `image_five`.
- [x] Aggiungere le cinque categorie immagini-con-testo con conteggio riferito alle immagini. Le relative famiglie sono `null` finché non vengono progettate e implementate.
- [x] Aggiungere `one_map` e `two_maps`; solo `one_map` espone la famiglia attualmente supportata.
- [x] Escludere le ricette legacy di stack dalla categoria `single_image`; mantenere il supporto compiler necessario ai piani già salvati.
- [x] Verificare test e JSON serializzato: i gruppi non implementati risultano esplicitamente `null`, non `[]` e non una lista generica condivisa.
- [x] Allineare `short_phrase` tra catalogo ChrononTemplate ed entry embedded di RenderingGen per le 15 motion `typewriter_modern_v1`; test mirati PASS e CLI: 38 scelte Short Phrase, 15 typewriter moderne, 397 motion, 22 famiglie e 17 use case.
- [ ] Implementare in tranche successive soltanto le famiglie richieste per ciascuna categoria, aggiornando catalogo e compiler insieme.
- [x] Dichiarare e sincronizzare i target canonici `map_view`: 10 motion `map_image_v1` mappa-only e 4 overlay centrati con target `image` + `map_view`. `sync_motion_catalog.sh --check` passa.
- [x] Ripristinare il build pulito dell’emitter correggendo la move-compatibility della `MotionScene` e due dichiarazioni accessor duplicate nelle modifiche locali ChrononMotion3D; il build pulito e l’emissione runtime delle recipe passano.
- [x] Conservare `image_stack_focus` per compatibilità compiler; non esporlo come categoria picker né nella famiglia `single_image`.
- [x] Semplificare il conflitto camera mappa/entity: il gate ora confronta controller dichiarati invece della presenza generica di `entity_style_id`. Due controller reali continuano a fallire con diagnosi nominativa; una mappa può convivere con uno stile entity senza motion camera. `go test ./internal/overlay ./internal/motion -count=1` PASS.
- [x] Rimuovere il controllo di categoria brush ridondante: dopo aver risolto la definizione basta confrontare la categoria richiesta dal lowering (`brush_v1`), senza un secondo catalogo di categorie che non cambia il risultato.
- [x] Spostare il catalogo delle composizioni immagini in `image_composition.go`, separando il modello semantico dal catalogo del picker. Il picker riusa la riga iterata per evitare una seconda ricerca; compiler e catalogo consumano la stessa fonte.
- [x] Rendere effettivo il limite 1–5 immagini nel compiler anche quando manca `composition_id`; il limite massimo è derivato dal catalogo, preservando l'opzionalità del campo per i piani già salvati.
- [x] Rimuovere l'ammissione hardcoded `category == image_premium_v1` dal lowering delle ricette: ogni `ImageRecipe` usa il lowering generico, mentre le quattro famiglie Visual Accents mantengono il proprio lowering specializzato. Una futura famiglia recipe non viene più esposta nel picker e poi scartata dal compiler per il nome della categoria.
- [x] Eliminare la seconda risoluzione Registry della stessa motion immagine durante il routing Visual Accents: la classificazione usa la definizione già caricata, mantenendo un'unica lookup in quel passaggio.
- [x] Rimuovere l’inferenza della family `typewriter` dal prefisso dell’ID: i 10 record legacy dichiarano ora `category: typewriter` nella sorgente ChrononTemplate e il registro usa la stessa proiezione per typewriter classico e moderno. Sync emitter/artefatto e test overlay/motion PASS.
- [x] Separare la capability renderer 3D dalle famiglie motion: rimosso il gruppo aggregato `3d` che duplicava motion di famiglie nominate; il requisito continua a essere esposto come `requires_3d`, mentre `text_3d_v1` resta una categoria autonoma. Test del catalogo runtime e del registry coprono entrambi.
- [x] Rimuovere il secondo layer `MotionFamilies()`/`FamilyMotionIDs()` e la mappa di alias category→family senza consumer runtime. Le pool di batch phrase leggono ora direttamente le sette categorie canoniche e `CategoryMotionIDs` esegue una sola corrispondenza esatta; i test mantengono 128 motion nel canary e 146 nella pool legacy.
- [x] Rimuovere i wrapper Registry che ripetevano solo `CategoryMotionIDs(category)`. Batch, preset ufficiali, gallery e test usano la query canonica; restano gli aggregati che definiscono pool reali (phrase planning e overlay image).
- [x] Unificare ammissione e lowering delle motion testuali e immagine: il compiler risolve il plugin una volta, verifica target e abbassa la definizione nello stesso passaggio, mantenendo la diagnostica item/motion/target. Rimosso il validatore a ID separato; gli stack recipe riusano la definizione già risolta e le mappe mantengono il gate del contratto per coprire anche il fly-to.
- [x] Portare plugin e definizione in un unico `resolvedMotion` durante il lowering immagine: i controlli recipe e l’animazione del layer riusano lo stesso risultato, inclusi entity card e composizione a layer; i child mantengono la propria risoluzione indipendente. I gate overlay/motion/overlaybatch passano.
- [x] Allineare il lessico interno al modello per conteggio: i predicati che riconoscono una recipe multi-immagine non la chiamano più categoria “image stack”; i vecchi motion ID restano compatibili nel catalogo.
- [x] Rimuovere `imageRecipeMotionDefinition`, wrapper che aggiungeva solo un filtro dopo la risoluzione canonica; il chiamante verifica direttamente i metadati della definizione risolta.
- [x] Rimuovere `runtimeMotionFamily` e il relativo clone profondo: non avevano chiamanti e duplicavano `RuntimeMotionFamilies`; l’API runtime pubblica resta invariata.
- [x] Rinominare il lowering recipe multi-immagine e il selettore del layer attivo con nomi semantici; i vecchi ID motion e il campo catalogo `stack` restano invariati per compatibilità.
- [x] Validare il contratto mappa una volta in `resolveSemanticItems`; `compileMapLayers` riceve l’item validato e si occupa solo del lowering. Il gate anticipato copre ancora mappe statiche e fly-to.
- [x] Allineare il contratto mappa all’animazione effettiva: `camera_move` controlla il fly-to; `motion_id` è opzionale e si applica alle mappe statiche. Gli eventuali valori già presenti nei piani fly-to vengono conservati/validati ma non applicati, per compatibilità. Questo permette anche composizioni senza famiglia dedicata.
- [x] Descrivere nel catalogo `one_map` la distinzione fra famiglia motion del basemap statico e fly-to via `camera_move`, rendendo la regola visibile ai consumer UI.
- [x] Usare `map_composition.go` come fonte unica per cardinalità e descrizione delle mappe nel catalogo runtime e nella validazione `map_composition_id`; verificati i casi 1/2/mancante/non supportato.
- [x] Evitare una seconda enum nel JSON Schema per `one_map`/`two_maps`: lo schema controlla il tipo stringa e il compiler valida gli ID contro il catalogo canonico.
- [x] Dichiarare il target motion direttamente sulle definizioni di composizione; il picker non associa più famiglie tramite `ID == single_image` o `MapCount == 1`, e i target vuoti continuano a generare `motion_ids: null`.
- [x] Chiarire la descrizione `single_image`: le scelte provengono da tutte le motion compatibili col target immagine, suddivise per family canonica, non da un’unica famiglia inventata.
- [x] Derivare `cardinality` e il prefisso descrittivo delle categorie immagine dai conteggi strutturati; le descrizioni mappa esistenti restano invariate per compatibilità.
- [x] Risolvere `kind` una sola volta per item e passarlo alla validazione delle composizioni immagini; rimosse le due interpretazioni kind/template interne che potevano divergere dal compiler.
- [x] Allineare anche la variabile di policy al significato `multiImageMotion`; il campo JSON legacy `ImageRecipe.Stack` resta l’unico residuo nel percorso di recipe per compatibilità col catalogo.

### Verifica aggiornata 7 ottobre 2026 (snapshot del worktree)

- `cd renderinggen && go test ./... -count=1` — PASS su tutti i package del modulo, inclusi overlay, processor e architecture.
- `make test-gofmt test-architecture && make conformance && git diff --check` dalla root RenderingGen — PASS (`gofmt: clean`, architecture PASS, `conformance: clean (3 targets)`).
- Il gate mirato successivo alle modifiche di ammissione/template — `go test ./internal/overlay ./internal/motion -count=1` — PASS.
- `RENDERINGGEN_SKIP_GPU_E2E=1 go test ./internal/overlay -count=1` — PASS.
- `RENDERINGGEN_SKIP_GPU_E2E=1 make test-unit` — PASS per RenderingGen, queue e objectstore.
- `make test-gofmt && make test-architecture` — PASS; `make conformance && git diff --check` — PASS.
- `GOWORK=off GOFLAGS= go build ./... && go vet ./...` — PASS nel modulo `renderinggen` (gli stessi comandi non sono stati rieseguiti qui per queue/objectstore).
- CLI `cmd/motion-catalog` nello snapshot aggiornato: schema v1, 397 motion, 23 famiglie selezionabili, 20 use case, i gruppi non implementati con `motion_ids: null`, nessun `image_stack` e nessuna motion multi-immagine in `single_image`. I precedenti conteggi 17/22 restano solo nei log delle verifiche storiche.
- Short Phrases: emitter C++ compilato, test contrattuale PASS, catalogo sorgente e artefatto Go byte-identici; 47 ricette catalogate e 23 product styles selezionabili dal runtime.
- `motionAdmitsTarget` usa solo i target dichiarati per phrase, caption, image e map. Il catalogo Short Phrases embedded è ora sincronizzato byte per byte con l’emitter C++ (47 ricette); le due ricette con `rotation` negli animator dichiarano `targets: null`, perché Chronon non le abbassa ancora, e il loader le omette dal registry runtime. Il numero delle ricette selezionabili deriva dai target e non da un conteggio hardcoded nel loader.
- Gate rieseguiti dopo le pulizie recipe/map: `cd renderinggen && go test ./... -count=1` — PASS; `make test-gofmt test-architecture && make conformance && git diff --check` — PASS (`conformance: clean`, 3 target). Il contratto mappa copre static/fly-to e il catalogo `one_map` descrive entrambi i percorsi.
- Verifica della composizione mappe a livello piano: `cd renderinggen && go test ./... -count=1` — PASS; `make test-gofmt test-architecture && make conformance && git diff --check` — PASS. `one_map`/`two_maps` derivano da `map_composition.go`; `map_composition_id` è opzionale e la validazione controlla la cardinalità senza invalidare piani legacy.
- Gate ripetuti sul catalogo dopo aver derivato le etichette dai conteggi: `cd renderinggen && go test ./... -count=1` — PASS; `make test-gofmt test-architecture && make conformance && git diff --check` — PASS (`conformance: clean`, 3 target).
- `cd renderinggen && go vet ./...` — PASS dopo la generazione runtime di cardinalità e descrizioni dal catalogo composizioni.
- Le modifiche concorrenti preesistenti a workflow/Makefile, cleanup plan e test architecture/editorial sono state preservate; nessuna UI o render GPU/Chronon è stato eseguito. T18 è stato rimosso dal perimetro per decisione owner; la certificazione GPU corrente resta non dichiarata.
- Aggiornamento catalogo picker: `images` espone `single_image`, `image_double`, `image_triplet`, `image_four` e `image_five`; `images_with_text` espone le cinque cardinalità corrispondenti. Le famiglie sono ammesse solo quando la definizione dichiara il target: oggi `single_image` ha motion, mentre le composizioni senza famiglia dedicata serializzano `motion_ids: null`. `one_map` e `two_maps` seguono la stessa regola; `image_stack` non è una categoria UI.
- Background è esposto come sorgenti plan-level supportate (`color`, `image`, `video`) nel modello di selezione; il catalogo runtime ora contiene 20 use case inclusi i tre background. Verifica dopo questo aggiornamento: `go test ./internal/overlay ./internal/motion -count=1` e `go test ./... -count=1` — PASS; `git diff --check` — PASS. La suite GPU non è stata eseguita.
- Le composizioni immagini, mappe e Background dichiarano ora la zona nella propria definizione canonica. `runtimeZones` e `runtimeUseCaseZone` leggono lo stesso campo; rimossi il controllo sul suffisso `_with_text` e il mapping hardcoded della zona per tipo di catalogo. Il test del modello verifica che ogni composizione appaia nella zona dichiarata; `go test ./internal/overlay ./internal/motion -count=1` e `git diff --check` — PASS.

### Modello di selezione nel catalogo runtime (tranche 7 ottobre 2026)

Il catalogo runtime non espone più solo famiglie e use case: pubblica un blocco
`selection_model` versionato e validato dallo schema, generato dalle stesse
definizioni che usa il compiler.

**Glossario pubblicato** — zona (area navigabile della libreria) ≠ `kind`
(primitiva semantica dell’item) ≠ template ≠ preset/stile ≠ motion. La zona si
deriva dalla definizione canonica di ogni composizione, il target dal catalogo
motion, il layout dal catalogo composizioni: nessuno dei tre viene ridichiarato
in una lista parallela.

Contenuti del payload:

- `zones`: le sei zone (`images`, `images_with_text`, `maps`, `background`,
  `data`, `camera_roll`) con le composizioni di ciascuna; `camera_roll` resta
  dichiarata senza composizione selezionabile.
- `compatibility`: matrice composizione × target × renderer capability, una riga
  per use case, con famiglie canoniche, conteggio motion, capability richieste
  (`2d`, `3d`, `camera`, `geospatial`) e motivo dell’indisponibilità.
- `layouts`: vincoli per ogni composizione immagine — aspect ratio
  `preserve_source_per_cell`, crop `cover`, safe area dal design token, gap,
  ordine `declaration_order`, selezione attiva per conteggio, asset mancante
  `fail_closed`, limiti e ordine di entrata delle caption e `motion_scope`
  (`item` per l’immagine singola, `per_layer` per 2–5, `per_caption` per le
  caption).
- `map_layouts`: basemap, pin, etichette, attribuzione e fly-to supportati con i
  target proiettati; `route`, `stops` e `callout` dichiarati non supportati con
  motivo.
- `background_sources`: sorgenti supportate derivate dal catalogo background con
  copertura `full_canvas`, fit, loop e priorità; `gradient`, `pattern` e
  `generated` dichiarate indisponibili.
- `camera_destinations` e `camera_policy`: destinazioni separate (scena, immagine,
  mappa) e regola del controller scena.
- `data_fields`: campi Metric/Date derivati dal catalogo composizioni e applicati
  ai blocchi opzionali `metric` e `date` di `overlay-plan.v1`; i blocchi dichiarati
  sono validati per target, campi obbligatori e limiti numerici.
- `ownership`: proprietario canonico di ogni tipo di dato consumato dal picker.

Verifiche di questa tranche:

- `go test ./internal/overlay -run TestSelectionModel -count=1` — PASS: zone,
  layout, matrice, layout mappa, sorgenti background, destinazioni camera e campi
  dati sono verificati contro il compiler. Le sorgenti background dichiarate
  supportate compilano, quelle indisponibili falliscono con `unsupported
  background kind`; l’ordine layer dichiarato è quello abbassato; una
  composizione con testo senza la caption richiesta fallisce.
- `RENDERINGGEN_SKIP_GPU_E2E=1 go test ./... -count=1` — PASS su tutti i package
  del modulo RenderingGen.
- CLI `go run ./cmd/motion-catalog`: payload schema-validato con
  `selection_model`; 397 motion, 20 use case, 10 layout composizione, 8 layout
  mappa.

Ancora aperto in questa area (non dichiarato PASS):

- Le famiglie dedicate per `image_double/triplet/four/five`, per le cinque
  composizioni con testo e per `two_maps` non sono autorate: la matrice le
  pubblica come indisponibili con motivo. Serve authoring in ChrononTemplate più
  certificazione renderer prima di esporre un ID.
- Lo stato di certificazione per motion è ora machine-readable nel payload
  (`selection_model.certification` più `motions[].certification`), derivato per
  embed dai report in `motion-certification/latest` e marcato `is_current: false`:
  descrive la run registrata, non la build corrente. Resta fuori dal payload il
  registro di preview è ora machine-readable come indice di puntatori (`preview`), non come elenco di media: i clip restano gitignored e vanno rigenerati dall’artefatto registrato.
- I blocchi `metric` e `date` sono opzionali per mantenere compatibili i piani
  che trasportano solo il testo già visualizzato. Quando un blocco è presente,
  schema, struct Go e validazione semantica richiedono i campi minimi, impediscono
  di applicare Metric a una data o viceversa, rifiutano campi sconosciuti e
  applicano i limiti dichiarati. `dataCompositionCatalog` è la fonte unica per
  ID, kind, cardinalità, descrizione, target e campi del picker; gli use case
  Metric/Date e i `data_fields` runtime sono derivati da questa definizione.
- Gate ripetuti dopo la dichiarazione canonica delle zone: `cd renderinggen && go test ./... -count=1` — PASS; `make test-gofmt test-architecture && make conformance && git diff --check` — PASS (`gofmt: clean`, architecture PASS, `conformance: clean (3 targets)`).
- Corretto un test di policy che chiamava `typewriter_clean` una motion legacy senza target, mentre il catalogo canonico dichiara `targets: ["text"]`. Il test ora verifica esplicitamente il target catalogato e non documenta più un fallback inesistente; i test mirati delle policy passano.
- Spostata la separazione Phrase/Short Phrase nella raccolta dei gruppi: una motion con target `short_phrase` entra in quel picker e non viene aggiunta anche a Phrase per poi essere sottratta con una seconda scansione. Il test runtime ora verifica la disgiunzione dei due elenchi; test catalogo mirato e `git diff --check` — PASS.
- Consolidata l’allowlist Visual Accents: i quattro ID canonici hanno una sola definizione privata; l’elenco esportato compatibile è derivato da quella sorgente e la validazione Go interroga la stessa membership. Aggiunto un test d’allineamento lista/validator; `go test ./internal/motion ./internal/overlay -count=1` e `git diff --check` — PASS.
- Rieseguita la CLI `go run ./cmd/motion-catalog` e ispezionato il JSON: 397 motion, 23 famiglie, 20 use case e sei zone. `image_double`–`image_five`, le composizioni con testo da 2 a 5 immagini e `two_maps` hanno `motion_ids: null`; `single_image` e `one_map` conservano solo i target dichiarati. Il conteggio precedente di 22 famiglie era uno snapshot storico, non il payload corrente.
- Rimossi `RuntimeMotionFamilies()` e `RuntimeMotionFamilyIDs()`: la ricerca completa dei call site ha trovato solo i test, mentre la CLI espone già famiglie e ID in `CompiledRuntimeAnimationCatalog`. I test ora interrogano gli helper interni di compilazione, senza duplicare un’API runtime; `go test ./... -count=1` — PASS su tutti i package; `make test-gofmt test-architecture && make conformance && git diff --check` — PASS (`gofmt: clean`, `conformance: clean (3 targets)`).
- Rimossi anche i wrapper non usati `RuntimeMotionCatalog()` e `RuntimeAnimationUseCases()`: la ricerca globale ha trovato solo test interni; CLI e consumer runtime usano il catalogo compilato completo. I test accedono ora ai builder privati per verificare le viste, senza mantenere ulteriori API di proiezione. `selection_model` usa i builder Camera e Dati dedicati presenti nel worktree (`runtimeCameraDestinations`, `runtimeCameraPolicy`, `runtimeDataFields`).
- Dopo la rimozione dei wrapper e l’allineamento dei builder: `go test ./... -count=1` — PASS; `make test-gofmt test-architecture && make conformance && git diff --check` — PASS (`gofmt: clean`, `conformance: clean (3 targets)`).
- Integrato il contratto strutturato Metric/Date: i blocchi JSON opzionali sono tipizzati nello schema e in Go; i payload dichiarati vengono validati contro target, obbligatorietà e limiti. Il catalogo Dati genera anche gli use case runtime, evitando copie di kind, cardinalità e descrizione. `go test ./internal/overlay ./internal/motion -count=1` — PASS.


---

## Lavoro residuo (ordinato per blocco, verifica 7 ottobre 2026)

Ordine di chiusura, non una seconda checklist: i criteri verificabili restano
nelle tabelle sopra. **BLOCCATO** = serve una decisione prodotto/owner, un
consumer UI esterno o infrastruttura non disponibile.

### Blocco 1 — Decisioni di prodotto / owner (chiuse il 7 ottobre 2026)

- [x] **T03** — deciso: i layout e le regole caption per conteggio sono derivate
      dal catalogo (`captionTextLimitLines`: 3 righe fino a 2 caption, 2 fino a
      4, 1 oltre; `caption_use_case: entity_caption` collega ogni composizione
      con testo all’use case che possiede le motion della caption). Conteggi
      immagini 1–5, mappe 1–2 e ownership pubblicata sono chiusi.
- [x] **T13** — chiuso come out-of-scope: la camera sull’intera scena non è
      pianificata. Il catalogo pubblica `camera_destinations.scene` con
      `requires_product_decision: false`, stato `unsupported` e motivo
      esplicito, senza contratto di movimenti e senza ID selezionabili
      (fail-closed). Il contratto speculativo a cinque movimenti è stato
      rimosso dal codice e dallo schema (che ora ammette `unsupported`).
- [~] **T16/T17** — **archivio locale eseguito e verificato** il 7 ottobre 2026
      con `scripts/archive_removed_mp4_duplicates.py`: 30 digest distinti, 30 path
      rimossi coperti, 30/30 ri-hashati e coincidenti, manifest
      `renderinggen.mp4-duplicate-archive.v1` in
      `<local-archive-root>/mp4-duplicates-20261007` (root host-local e non versionato),
      restore test eseguito (hash identico + probe H.264 1920×1080 5.000 s).
      Restano `PENDING` solo le voci che l’owner deve firmare: owner
      accountable, policy/periodo di retention, audit dei consumer esterni,
      decisione e approvatore. L’archivio è locale e fuori dal repository, quindi
      `external_archive_verified` resta `false`: non è un archivio indipendente.
- [x] **Fase 4** — deciso: **deprecazione immediata**, nessuna finestra di
      compatibilità e nessun alias. Il catalogo pubblica la policy
      (`selection_model.deprecation`) e il compiler rifiuta un ID ritirato prima
      del lowering, con diagnosi stabile. Nessun motion canonico è marcato: la
      procedura resta pronta per il primo ritiro reale.

### Blocco 2 — UI (fuori perimetro)

- [x] **T18 rimosso** per decisione owner del 7 ottobre 2026: la UI non è una
      consegna di questo repository, che pubblica il catalogo versionato e il
      modello di selezione come interfaccia consumabile dall’esterno.
- [x] **Fase 2 (ordine di selezione e presentazione)** e **Fase 6 (rimozione dei
      duplicati dalle UI)** decadono con T18: non resta nessun criterio UI da
      chiudere in questo repository.

### Blocco 3 — GPU / certificazione (riprodotto il 7 ottobre 2026)

- [x] **Riproduzione del gate strict** — il gate è stato eseguito su questo host
      con l’engine reale (`Chronon3d 0.1.0`, RTX A4000, backend `vulkan`,
      encoder `nvenc` native): 20 motion immagine premium, **3 passate, 16
      `render_failed`, 1 `compile_failed`**, timestamp
      `2026-10-07T11:23:50Z`. Evidenza in
      `evidence/gpu-strict-image-run-20261007.v1.json` con comando, controllo
      (`hardware=none` → 20/20 falliscono in 5 s: la corsia strict richiede
      NVENC) e **analisi della causa**: `EncoderFailed` è solo il sintomo, perché
      nessun frame arriva all’encoder. Le due cause reali, riprodotte anche in un
      processo isolato (`RENDERINGGEN_GPU_CERT_MOTION=<id>`):
      1. `UnsupportedCapability` del dispatcher nativo Vulkan, che implementa solo
         grid, path fill, path stroke e solid rect: ogni altra shape avrebbe
         bisogno del fallback legacy, che la corsia `require_gpu_native` non
         allega per progetto. `image_corner_bloom` fallisce da solo emettendo
         quattro layer `ellipse`; le tre che passano emettono un solo `path`.
      2. **Vulkan device loss** durante l’handoff CUDA/Vulkan
         (`vkWaitForFences(CUDA export slot)`, poi “GPU worker is poisoned”):
         `image_card_flip_soft` fallisce da solo con tre report di device loss e
         nessun `UnsupportedCapability`.
      Sono difetti engine (Chronon3d), non errori semantici di RenderingGen.
- [x] **Bug dell’harness corretto** — il `compile_failed` di `image_stack_focus`
      veniva dal piano di test, non dal lowering: dichiarava `entity_caption` sul
      padre composito, che il contratto rifiuta. La caption ora sta sul layer
      proprietario (`catalog_motion_gpu_test.go`).
- [ ] **Certificazione strict corrente** — resta non soddisfatta e ora ha un
      owner nominato: Chronon3d deve coprire (o accettare un fallback per) le
      shape che le motion immagine emettono, e risolvere il device loss
      nell’handoff CUDA/Vulkan. Finché non accade, nessuna di queste motion è
      certificabile e nessuna è presentata come certificata nel catalogo.
- [ ] **T07 pixel densi** — la collisione caption è coperta a livello di
      compilazione; resta la verifica pixel dei layout densi.

### Blocco 4 — Lavoro in questo repository

- [x] **Composizioni contate** — `image_double/triplet/four/five` e le cinque
      `*_with_text` riusano le motion del target `image` applicate **per layer**
      (`motion_scope: per_layer`) invece di attendere una famiglia per conteggio:
      è il contratto a layer già supportato dal compiler, quindi il picker non
      espone nessuna motion composta non abbassabile (134 scelte per
      composizione, 134 anche per le composizioni con testo sul lato immagine).
      `two_maps` riusa allo stesso modo le motion `map_view` per placca (14
      scelte). Le famiglie dedicate per conteggio restano un’opzione editoriale,
      non un prerequisito.
- [x] **Famiglia camera scena** — chiusa come non pianificata (vedi T13):
      nessun authoring, lowering o certificazione da attendere; la destinazione
      `scene` resta pubblicata come `unsupported` e fail-closed.
- [ ] **Selezioni batch** — il catalogo categorie è la query canonica, ma alcune
      pool batch restano selezioni editoriali intenzionali e richiedono un audit
      consumer separato.
- [x] **Fase 5** — auditato (T20b): nessuno spostamento sicuro senza
      rifattorizzare le API di test e i call-site; gli harness restano test-only
      nello stesso package che CI invoca e i generatori di corpus sono già in
      `cmd/`. Unificare report/gallery resta subordinato a schema e gate comuni.
- [ ] **Fase 6** — test/gallery one-off da trasferire in fixture/manifests
      riproducibili; video/log di prova fuori da Git con report, hash, procedura di
      recupero e riferimenti CI; schema/campi obsoleti solo dopo deprecazione e
      prova che nessun produttore li invia.
- [~] **Registro preview** — il ledger machine-readable esiste
      (`motion-preview/registry.v1.json`, scritto da `cmd/preview-registry`): per
      motion pubblica l’artefatto che dichiara la preview e il clip solo quando la
      gallery lo nomina meccanicamente. I clip restano fuori da Git per policy
      (`.gitignore`), quindi il ledger è un indice di puntatori: per mostrare
      un’anteprima il consumer deve rigenerarla dall’artefatto. Resta da
      rieseguire il generatore quando nascono nuove gallery.

### Verifiche di questa tranche

- [x] **Registro di preview per motion machine-readable**: il package
      `motion-preview` (embed) espone il ledger scritto da `cmd/preview-registry`,
      che scansiona gli artefatti di gallery e batch (`out`, `preset_videos`,
      `phrase_preset_videos`, `typewriter_phrase_videos`,
      `apple_style_final_videos`) e registra per motion l’artefatto che dichiara la
      preview più il clip quando il nome è meccanico. Il catalogo pubblica
      `selection_model.preview` (fonte, `generated_at_utc`, root scansionati,
      `media_in_git: false`, conteggi, regola) e `motions[].preview`
      (`recorded`/`none` con artefatto e media): snapshot corrente 68 motion
      registrate, 329 senza, 748 righe di ledger.
- [x] Verifica di questa tranche: `go test ./internal/overlay ./motion-preview
      ./cmd/preview-registry ./internal/motion -count=1` — PASS; `go vet` sugli
      stessi package — PASS; ratchet export morti — PASS; `git diff --check` —
      PASS; lo schema chiuso v1 accetta il payload esteso (`motion_preview`,
      `preview`).

- [x] **Stato di certificazione per motion machine-readable**: il package
      `motion-certification` (embed) parsa i report checked-in e il catalogo
      pubblica `selection_model.certification` (fonte, `snapshot_at`,
      `is_current: false`, regola, conteggi, report) più `motions[].certification`
      (`status` in `passed`/`failed`/`unverified`, report, data, errore). Un motion
      che nessun report nomina è `unverified`, mai “certificata”.
- [x] CLI `go run ./cmd/motion-catalog`: 397 motion, 117 `passed`, 11 `failed`,
      269 `unverified`, snapshot `2026-10-02T19:11:03Z` su tre report; lo schema
      chiuso v1 accetta il payload (test di schema-parity PASS).
- [x] `go test ./internal/overlay ./motion-certification -count=1` — PASS;
      `go vet ./...` — PASS; ratchet export morti (`TestNoDeadExports`) — PASS con
      il nuovo package.
- [x] **Procedura motion-deprecation**: aggiunti schema chiuso proposta e report, CLI read-only con confronto consumer/target/finestra, registry-state testabile e filtri picker; nessuna motion di produzione è stata marcata. Fixture CLI genera `PASS_AUDIT_ONLY` (date future fisse), mismatch consumer/target e finestra scaduta generano `BLOCKED` con exit 2 nei test end-to-end. I report generati si validano contro lo schema. `go test ./cmd/motion-deprecation-audit ./internal/motion ./internal/overlay ./internal/architecture/... -count=1` e `go vet` mirato — PASS. Il compilatore lascia risolvibili i piani salvati fino a `remove_after`; dalla data in poi segnala in modo deterministico l’ID, ragione e replacement senza fallback.
- [x] **Retention media**: audit read-only `python3 scripts/audit_removed_mp4_retention.py --inventory CLEANUP_ARTIFACT_INVENTORY.csv --output evidence/mp4-retention-audit.v1.json` — PASS sui 30 path: byte storici 30/30, almeno una copia identica per ognuno, 17.898.958 byte totali per righe rimosse, ffprobe H.264 1920×1080/durata positiva, path assenti e nessun riferimento letterale corrente. Sign-off esterno resta pending (`external_archive_verified: false`). `--self-test` — PASS.
- [x] **Harness**: verifica call-site/package/CI: `certification_harness_test.go` deve restare nel package test overlay per ora; 78 helper/declaration package-local, dipendenze usate da test normali e tagged, entrypoint CI invariato. Nessuno spostamento effettuato perché non isolabile con un move sicuro.
- [x] Il GPU runtime-certification è stato **eseguito** su questo host con l’engine reale (`CHRONON_BIN` → build Chronon3d del 6 ottobre): 3/20 motion immagine passano, 16 `render_failed` con firma `EncoderFailed`, 1 `compile_failed` da bug dell’harness (corretto). Evidenza in `evidence/gpu-strict-image-run-20261007.v1.json`. Restano blocchi esterni l’archivio indipendente per la retention e le decisioni già chiuse dall’owner.
- [x] Corretto il gate `entity_style_id`: limitava gli stili a `entity_card`, ma
      il compiler abbassa anche `organization`, `location` e `concept` con lo
      stesso percorso entity. Ora le quattro kind ammettono il catalogo entity;
      un test compila tutte le 25 varianti per tutte e quattro (100 combinazioni)
      e verifica che motion immagine e caption siano ammesse dai rispettivi
      target. Build `34e98b9f…` installata nel worker (`/health`: ready); quattro
      render Vulkan di Milton Leite completati, uno per kind, stesso artifact
      SHA-256 `2bba4a5964ae03e4c2b57ea95e11dfed29a0d5e203d828d10d72a164c9d59f77`.
      La clip campione è `out/entity-runtime-family-smoke-milton-leite-20261007.mp4`;
      frame a 0,25 s, 1 s e 2 s differiscono per 163–178 mila pixel. `go test
      ./internal/overlay -count=1` e `git diff --check` — PASS.

---

## Chiusura perimetro repository (7 ottobre 2026)

Questa tranche chiude ciò che il repository può chiudere da solo e rimuove dal
perimetro ciò che l’owner ha scartato.

### Decisioni eseguite

- **T18 rimosso** con tutte le voci UI (Fase 2 selezione, Fase 6 duplicati UI):
  nessun criterio UI resta in questo repository.
- **T13 chiuso come out-of-scope**: la camera sull’intera scena non è
  pianificata. Il catalogo espone la destinazione `scene` con
  `requires_product_decision: false`, stato `unsupported` e motivo esplicito,
  senza contratto di movimenti e senza ID selezionabili (fail-closed).
- **Fase 4 decisa e implementata**: deprecazione **immediata**, nessuna finestra e
  nessun alias (`selection_model.deprecation`), picker escluso e compiler che
  rifiuta un ID ritirato prima del lowering con diagnosi che nomina ID, motivo e
  replacement.
- **T16/T17**: attribuzione registrata, archivio esterno ancora `PENDING`.

### Chiusure tecniche di questa tranche

- **Composizioni contate** — `image_double`, `image_triplet`, `image_four`,
  `image_five` e le cinque `*_with_text` espongono le 134 motion del target
  `image` applicate per layer (`motion_scope: per_layer`), invece di restare
  `null`: è il contratto a layer che il compiler già abbassa, quindi nessuna
  motion composta non supportata viene mostrata.
- **`two_maps`** — espone le 14 motion `map_view` applicate per placca; il
  lowering è per item mappa, verificato dai test esistenti.
- **Caption dense** — `captionTextLimitLines` deriva il limite righe dal numero
  di caption (3 → 2 → 1) e ogni composizione con testo pubblica
  `caption_use_case: entity_caption`, così le motion della caption restano
  nell’use case che le possiede invece di duplicare liste.
- **Harness GPU** — corretto il piano di `image_stack_focus`, che dichiarava una
  caption sul padre composito: il contratto la vuole sul layer proprietario.

### Verifica

- `go test ./internal/overlay ./internal/motion -count=1` — PASS.
- `go test ./internal/overlay -run 'TestDeprecation|TestCameraRoll|TestSelectionModel|TestRuntimeAnimation' -count=1` — PASS.
- CLI `go run ./cmd/motion-catalog`: 397 motion, 23 famiglie, 20 use case, sei
  zone; `image_double`…`image_five` e le composizioni con testo espongono 134
  scelte per layer, `two_maps` 14 per placca, le tre `background_*` restano
  `null`; `selection_model.deprecation` pubblica `policy: immediate`,
  `compatibility_window_days: 0`, `aliases_allowed: false`; lo schema chiuso v1
  accetta il payload (test di schema-parity).
### Verifica finale dei gate (7 ottobre 2026, 11:26–11:31 UTC)

| Gate | Esito |
|---|---|
| `make test-gofmt` | PASS (`gofmt: clean`) |
| `make test-architecture` | PASS |
| `make conformance` | PASS (`clean`, 3 target) |
| `git diff --check` | PASS (nessun errore) |
| `go vet ./...` (modulo renderinggen) | PASS |
| `make test-module-standalone` | PASS (`standalone builds: ok`, tre moduli) |
| `RENDERINGGEN_SKIP_GPU_E2E=1 go test ./... -count=1` | 1 solo fallimento, esterno a questa tranche (vedi sotto) |
| Gate strict GPU (engine reale) | 3/20 `passed`, 16 `render_failed` (`EncoderFailed`), 1 `compile_failed` risolto |

Aggiornamento delle 11:44 — il fallimento esterno è stato **chiuso**:
`TestFeatureFlyToLowersByNaturalEarthNameUsingOfflineLODs` dichiarava due LOD
offline allo stesso zoom (`lods[1].zoom = 4`), quindi il range 4..4 non poteva
soddisfare il fly-to per nome (il resolver richiede `finalLODZoom > startZoom`).
Il fixture è stato corretto (`lods[1].zoom = 5`) e l’intero modulo RenderingGen è
verde: `RENDERINGGEN_SKIP_GPU_E2E=1 go test ./... -count=1` — PASS.

Aggiornamento delle 11:48–12:01 — durante la finestra la sessione concorrente ha
lasciato per alcuni minuti `internal/overlay/map_route.go` e
`semantic_map_test.go` in stato non compilabile; i gate sono stati rieseguiti
dopo e sono **tutti verdi alle 12:01:24**: `make test-gofmt` (0),
`make test-architecture` (0), `make conformance` (0, `clean` 3 target),
`git diff --check` (0), `make test-unit` (0), `make test-module-standalone` (0),
`go vet ./...` (0), `RENDERINGGEN_SKIP_GPU_E2E=1 go test ./... -count=1` (0).
Nella stessa finestra sono stati formattati con `gofmt -w` due file della
lavorazione concorrente (`compiler.go`, `map_route.go`): modifica di soli spazi,
richiesta dal gate `test-gofmt`.

### Stato rilevato successivamente nel checkout (7 ottobre 2026)

**Aggiornamento 7 ottobre (sera) — risolto.** La non compilabilità segnalata
sotto (`compileMapRoutes` a 8 vs 9 argomenti, `*float64` per
`trimStart`/`trimEnd`) è stata riparata dalla lavorazione concorrente. Verifica
attuale su questo checkout: `go build ./...` e `go vet ./...` PASS;
`RENDERINGGEN_SKIP_GPU_E2E=1 go test ./... -count=1` PASS su tutti i package;
CLI `go run ./cmd/motion-catalog` — schema v1, 397 motion, 6 zone, 20 use case,
certificazione snapshot `2026-10-02T19:11:03Z` (`is_current: false`), preview
68 registrate / 329 senza. Audit retention rieseguito self-consistente:
`all_paths_have_no_current_textual_reference: True`, report aggiornato in
`evidence/mp4-retention-audit.v1.json` (baseline commit `b0dd7253`, 30/30 hash
e copie identiche confermate). Il ledger preview
(`motion-preview/registry.v1.json`) resta lo snapshot committato: una
rigenerazione locale trova le root di scan vuote (i clip sono gitignored per
policy) e va rieseguita solo quando nascono nuove gallery.

Storico della verifica incrociata: una suite
`RENDERINGGEN_SKIP_GPU_E2E=1 go test ./... -count=1` è terminata PASS, ma in un
comando successivo la CLI `go run ./cmd/motion-catalog` e il test mirato overlay
non compilano a causa di modifiche concorrenti alle mappe. In particolare,
`semantic_map.go` chiama `compileMapRoutes` senza il nuovo argomento
`SemanticMapPoint`; `map_route.go` tratta invece `trimStart`/`trimEnd` come
`*float64` con operazioni da `float64`. Queste modifiche non sono state
alterate in questo aggiornamento. La suite PASS precedente non prova il
checkout dopo tali variazioni: riallineare la firma e i tipi, quindi rilanciare
suite, CLI e schema-parity prima di dichiarare verdi i gate finali.

`make test-gofmt test-architecture`, `make conformance` e `git diff --check`
sono passati in questa verifica. L’audit MP4 read-only trova hash baseline e
copie identiche corretti (30/30), path rimossi assenti e nessun archivio esterno
verificato. Lanciato con report di output temporaneo non escluso, segnala anche
il report storico checked-in come riferimento testuale dei 30 path; eseguire
l’audit con il percorso JSON standard escluso dal detector prima di valutare
consumer reali.

### Ancora aperto (fuori dal controllo di questo repository)

- **Identità del repository (già decisa, azione esterna)** — il nome canonico
  è **RenderingGen** (capital R, capital G): è il module path Go dei tre moduli,
  il nome del repo remoto atteso e la regola `module_path_typo` della
  conformance, che rifiuta ogni variante con refuso. Il remote configurato in
  questo checkout porta una variante refusa del nome, quindi la correzione è un
  **rename del repository su GitHub da parte dell'owner**: nessuna modifica di
  import, `go.mod` o codice è corretta o necessaria qui, e nessuna è stata
  fatta. Non reintrodurre la variante refusa in codice, `go.mod` o config.
- **Famiglia camera scena** — authoring in ChrononTemplate
  (`camera_roll/SceneCameraPack.hpp`) più lowering RenderingGen su
  `camera_animation` e certificazione: prerequisito per rendere selezionabile la
  destinazione `scene`.
- **Certificazione strict GPU corrente** — 3/20 rendono; i 16 fallimenti sono
  `EncoderFailed`, quindi serve un percorso encoder stabile e un sign-off.
- **Retention** — archivio indipendente e approvazione firmata.
- **Fase 6** — fixture/manifests per i test one-off e schema/campi obsoleti solo
  dopo deprecazione e prova che nessun produttore li invia.
