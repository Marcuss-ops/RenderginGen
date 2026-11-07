# TICKET — Confine Producer 77 / Master 51 e piano finale immutabile

- **Data:** 2026-11-04
- **Componenti:** PipelineGen (Producer 77), RenderingGen (overlay), Master 51 (worker remoto)
- **Contesto:** job editoriale Garlasco da 20 minuti, run r10–r37
- **Stato:** r37 chiuso con MP4 verificato (2026-11-04 21:16). **2026-11-05 — decisione finale:** il video finale è sempre e solo remoto; il Producer prepara i frammenti (stock incluso), l'audio finale e il piano immutabile, poi li invia all'assembler. Nessun render finale locale. I workaround direct-source (gate strict, preflight/allowlist host, boundary receipt) sono stati rimossi perché il worker `video.assemble.copy.v1` è copy-only e non può accettare sorgenti dirette.

> **Aggiornamento 2026-11-07 — prevale su ogni sezione storica:** lo stock non viene mai processato in locale prima del submit. Il Producer invia solo il riferimento Drive + metadata salvati dalla `stockpipeline`; il Master remoto scarica e processa. Le sezioni che descrivono download, probe, taglio o pubblicazione stock locale sono storiche e superate. Dettaglio in fondo al ticket.

## Aggiornamento richiesto — stock Drive diretto (2026-11-05)

La decisione di mantenere il download stock nel Producer è superata: per i final job il Producer deve inviare il riferimento Drive e metadata già salvati dalla `stockpipeline`, senza scaricare, sondare, calcolare hash o pubblicare byte stock. Clip/overlay/audio prodotti dal Producer continuano a viaggiare come asset HTTPS. La pipeline stock resta attiva per acquisire, modificare e salvare lo stock.

Il Producer ora risolve durata/hash dalla media registry e dimensione dai soli metadati Drive; il piano conserva i segmenti stock con `drive_file_id` e la preparazione locale li salta. Restano da verificare l'accettazione del riferimento da parte del Master/worker e un job end-to-end: il contratto storico descriveva `video.assemble.copy.v1` come copy-only e il test live non è stato rieseguito dopo questo cambio.

## Regola di perimetro

Il video finale è **sempre e solo remoto**. Il Producer non deve:

- creare, concatenare o pubblicare un video finale in locale;
- caricare i byte stock originali sul Master/Objectstore.

Il Producer fa la sua parte: render overlay, TTS/voce, audio finale certificato, taglio dei **soli frammenti proprietari** (clip/overlay) e **un unico piano immutabile** (`compiled_render_plan_json` + SHA-256). I frammenti sono pubblicati via HTTPS Objectstore e l'assembler remoto li concatena con `video.assemble.copy.v1`; lo stock originale resta remoto e non attraversa il confine. **Dal 2026-11-07 il taglio stock non è più un contributo del Producer.**

Questa è la decisione del 2026-11-05 dopo il confronto con il worker 51: l'executor disponibile è copy-only e non accetta sorgenti dirette, quindi un piano di sole sorgenti non è realizzabile. I workaround introdotti per simulare quel confine (gate strict, preflight/allowlist host, boundary receipt) sono stati rimossi: non spetta al Producer fingere una capacità che il worker non ha.

**Superato il 2026-11-07.** La risoluzione stock non scarica più i byte: legge la media registry e i metadata Drive, e il preparer non riceve mai `role=stock`. Il piano dichiara durata e identità già salvate dalla `stockpipeline`; il download e il taglio avvengono solo nel Master.

## Esito r37 — storico pre-fix (2026-11-04; superato dall'esito finale sotto)

- Job locale: `job_1793815155700231349_108231b6` (payload `r37-20min-20261104-single-plan-no-stock-upload`).
- Job remoto: `job_4f2831e8631752bc`, creato alle 18:04:50; resta `PENDING`/`prefetch_queued` con `started_at=null` per oltre 50 min. Nessun MP4 finale: il render non è mai partito.
- Il PREPARE del Master ha **accettato** il piano (`future_asset_plan=sent`): nessun `source_range_out_of_bounds`, nessun `m2m_quota_failure`. La regressione r36 non si è ripresentata.
- Il job locale è finito `FAILED` alle 18:54:47 ed è in dead-letter queue (`retryable=true`) per un HTTP 500 **transitorio** del Master durante il poll: `m2m_auth_failure` / `M2M key lookup failed`. La stessa chiave ora risponde 200 sia su `GET /api/v1/jobs/job_4f2831e8631752bc` sia su `GET /api/v1/jobs/types`: non è una regressione del Producer né una chiave ruotata.
- Tempi misurati: handoff locale completato in **5m35s** (17:59:16 → 18:04:50); primo poll fermato dopo **3m03s** perché il Master non aveva ancora avviato il job; il retry ha riusato gli artifact e ha ripollato in ~1m30s. Il costo locale non è il collo di bottiglia.
- Binario verificato: `74bb5e0ef95d2a771368a1ef6521c7bc97796032f4a9aa1b3fa0f366a97c49f8` (health e `/proc/<pid>/exe` coincidono).

### Blocco prefetch r37 — worker allowlist

- Il job remoto `job_4f2831e8631752bc` resta `PENDING`/`prefetch_queued` (`future_asset_plan=sent`, `started_at=null`).
- Prefetch registrato dal Master: **198 asset su 199 falliti a 0 byte** con `worker direct asset source rejected`; l'unico asset riuscito è quello Drive (stock), mentre tutti gli asset objectstore falliscono.
- Worker: `host_57_129_132_133`, `CONNECTED`, heartbeat recente, 2/2 slot liberi, engine `v1.4.52`, executor `video.assemble.copy.v1`. L'admin API `/api/v1/workers` non espone la source-host allowlist né un endpoint di config.
- Producer: `OBJECTSTORE_PUBLIC_URL=https://77.93.152.122`; certificato pubblico con SAN IP `77.93.152.122` valido fino all'8/11/2026; listener HTTPS attivo (401 senza firma, come da contratto). L'host coincide con quello che `RenderingGen/infra/docker/OBJECTSTORE-PUBLIC-HTTPS.md` dichiara come allowlist del worker.
- Il Master **non fa proxy** dei byte (`OBJECTSTORE-PUBLIC-HTTPS.md`: "The Master does not proxy the media bytes"). Non esiste un percorso "download tramite Master" da attivare nel manifest: il manifest è già conforme al contratto (`url=velox-asset://…`, `source_uri=https://77.93.152.122/objects/…` firmato).
- Conclusione: il blocco è la source-host allowlist del worker v1.4.52. Se la allowlist contiene già l'IP, il secondo sospetto è il mapping Master→worker: il worker deve ricevere `source_uri`, non `url` (`velox-asset://`).
- Fix immediato: aggiungere `77.93.152.122` (HTTPS, porta 443) alla allowlist del worker e ricaricarlo; verificare che il prefetch passi a 199/199 e che `started_at` diventi non-null. Se continua a fallire, loggare l'URL esatto rifiutato nel prefetch del worker/Master: il log attuale non conserva l'host.
- Non rilanciare il job locale prima di quella verifica: il prefetch fallirebbe identico. Il job remoto è ancora valido; dopo il fix un retry locale riusa gli artifact e si riattacca.

## Esito finale r37 — MP4 consegnato (2026-11-04 21:16)

- **Job locale:** `job_1793826770287488401_64fd7f93` → `SUCCEEDED`.
- **Job remoto:** `job_438bc0e18f9f6dd8` → `SUCCEEDED` (21:16:03 → 21:16:14), prefetch **188/188 ready**.
- **Artefatto:** id `22406a5b5a90b21b044d8349b8553f44`, **349.535.157 byte**, SHA-256 `93d72981ec8c1b7026fc002de125eb8acad33a330538a5794da326f2c37a2434`.
- **URL:** `https://veloxmanager.duckdns.org/api/internal/artifacts/22406a5b5a90b21b044d8349b8553f44/download` (richiede il token admin).
- **File locale:** `ops/jobs/garlasco_editorial_e2e_20261030/r37-20min-20261104-final.mp4`.
- **ffprobe:** durata `1171.751 s`, H.264 1920×1080 a 24/1, AAC stereo; size e SHA coincidono con l'artefatto remoto.
- **Piano:** 265 segmenti, 266 asset (265 video + 1 audio), 0 binding errati, 0 `source_range_out_of_bounds` al PREPARE.

### Causa e correzioni

1. `BuildFinalJobPayloads` non invocava `prepareFinalJobVideoSegments`: il Master validava il piano grezzo e rifiutava 24 segmenti con `source_range_out_of_bounds` (job `job_1793825645902202468_f51650c7`). Aggiunto `BuildFinalJobPayloadsWithSegmentPreparer`, chiamato dall'adapter; il piano viene sigillato dopo il taglio dei frammenti.
2. `alignFinalJobCompiledPlanAssetIDs` azzerava `source_in_us` prima del taglio: ora lo preserva; il preparer lo azzera solo sul frammento già tagliato.
3. `asset_key` era `sha256:<sha>` mentre il worker prenotava lo SHA nudo: il prefetch e il lease divergevano (`worker.reserveJobAssetProtection ... cached asset not found`, job `job_1793826115491415683_940f9d12`). Ora `asset_key` è l'`asset_id` canonico, con SHA nudo.
4. Gli overlay video-only vengono certificati V1 nel frammento; timeline, durata e frame vengono dal piano. I test `TestAlignFinalJobCompiledPlanAssetIDs` e `TestPrepareFinalJobVideoSegments` sono verdi.

### Debito architetturale — risolto dalla decisione del 2026-11-05

Il MP4 è consegnato; per farlo passare dal worker copy-only è stato **riattivato il preparer locale**: 265 frammenti video (237 stock, 25 overlay, 3 clip) sono stati tagliati e pubblicati su objectstore. Con l'executor `video.assemble.copy.v1` questo non è un workaround da eliminare: è il contributo del Producer. La regola "il Producer non produce frammenti" è stata ritirata; restano vincolanti "nessun video finale locale" e "nessun upload dei byte stock originali". Dettaglio nella sezione 2026-11-05.

## 2026-11-05 — decisione finale: frammenti al Producer, film al remoto

Il confine non si chiude con un executor che non esiste. Il worker configurato
(`video.assemble.copy.v1`) concatena solo frammenti certificati; il video finale
resta quindi interamente remoto, mentre il Producer prepara i pezzi. I
workaround direct-source sono stati rimossi dal codice.

### Video finale — CHIUSO

- Nessun percorso locale di assembly: il flusso `videocreate` è stato rimosso
  (commit `630915e28`) e `finalJobSegmentPreparer` produce solo frammenti, mai il
  film. I test `runner_final_video_boundary_test.go` e
  `payloads_no_final_video_flag_test.go` vietano la reintroduzione.
- Il Producer invia piano immutabile + frammenti/overlay/audio; il worker remoto
  assembla e pubblica l'artefatto finale.

### Stock sola lettura — storico (superato il 2026-11-07)

> Questa sezione descrive il vecchio contratto con `local_source_path`/`local_only` ed è stata superata: dal 2026-11-07 il Producer non riceve né usa path stock locali.

- `resolveFinalJobStockAsset` non pubblica i byte stock originali quando il preparer è configurato: restituisce `local_source_path` + `local_only`, con SHA-256, size e durata già certificati.
- `stagedSource` verifica size e SHA-256 del file Creator-local e lo usa direttamente: nessun download, nessuna copia, nessun upload dell'originale. Il file sostituito o manomesso è rifiutato.
- Il ref preparato elimina `local_path`, `local_source_path` e `local_only`: il path locale non attraversa il confine né finisce nel piano inviato al Master.
- Prefetch e handoff condividono lo stesso percorso read-only; il prewarm non può più pubblicare lo stock come effetto collaterale.

### Errore piano — CHIUSO lato Producer, aperto lato Master

- `validateFinalJobPlanBindingRules` rifiuta `source_in_us + source_duration_us > asset.duration_us` prima di PREPARE, con coordinate `video_tracks[i].segments[j]`, valori osservati e durata dichiarata, sotto `invalid_compiled_render_plan`.
- `remotejob` riconosce l'envelope reale `m2m_quota_failure` + `source_range_out_of_bounds` e lo converte in `PlanValidationError` / `ErrInvalidCompiledRenderPlan`, terminale (`IsRetryable() == false`). Un vero esaurimento quota resta distinto.
- **Residuo Master 51:** il server continua a rispondere `m2m_quota_failure`; il Producer ora non lo interpreta più come transiente, ma il codice corretto va prodotto lato Master.

### Workaround rimossi — CHIUSO

- Rimosso `VELOX_FINAL_JOB_REQUIRE_DIRECT_SOURCES` e `ErrFinalJobDirectSlicingUnsupported`: non c'è un percorso diretto da proteggere, quindi il gate non aveva più un oggetto.
- Rimosso il preflight/allowlist `OBJECTSTORE_PUBLIC_URL` / `VELOX_FINAL_JOB_SOURCE_HOSTS`: l'allowlist è configurazione del worker, non una decisione del Producer; un mismatch si diagnostica dal prefetch remoto.
- Rimosso `RemoteFinalJobTiming.Boundary` e i conteggi per ruolo: il confine è dichiarato dalla decisione architetturale, non misurato come una violazione a ogni run.

### Report owner — CHIUSO

- Il receipt viene proiettato in `RunReport.FinalJob` e persisted in `run_observability.report_json`.
- `workflow_payload_json` resta il checkpoint ripristinabile, non una seconda superficie di report.
- Il receipt porta `report_owner=run_observability.report_json` su submit, attach, pending e stale.

### Verifica eseguita

```text
go test ./internal/platform/remotejob        PASS
go test ./internal/capabilities/scripts     PASS
go test ./internal/app/wiring               PASS
go test ./internal/kernel/observability     PASS
```

## Criterio ritirato

Il criterio "nessun frammento video" contraddiceva sia il worker copy-only sia il vincolo di non creare il film in locale. Con la decisione del 2026-11-05 il taglio dei frammenti è il contributo del Producer; restano vincolanti "nessun upload dei byte stock originali" e "nessun video finale locale".

## Errori osservati

| Run | Sintomo | Causa | Stato |
| --- | --- | --- | --- |
| r10 | immagini non verificate per 6/8 entità | gate immagini | chiuso |
| r11 | segmento vuoto nel piano | finestra a 0 frame | chiuso |
| r12 | overlay a 1,35 s fuori dai confini GOP | copy-only rifiutava il taglio | chiuso (packing su GOP) |
| r13 | clip stock senza GOP chiusi certificabili | planner scartava la sorgente | chiuso (fallback ricodifica certificata) |
| r14 | FFmpeg `signal: killed` al segmento 247 | 4 lane in parallelo | chiuso (1 lane) |
| r15 | `avcC` non canonico al segmento 121 | SAR 20630:20633 non rilevato | chiuso (gate SAR 1:1) |
| r16/r17 | MP4 non decodificabile ai confini di clip | packet-copy di frammenti con riferimenti rotti | chiuso (ricodifica completa) |
| r17/r18 | `FAILED` locale dopo render remoto riuscito | spazio disco < 2 GiB nel finalizer | chiuso (pulizia cache) |
| r18–r20l | testo in alto a sinistra, highlight lontani | `screen_space` perso, `alignment=center` perso, marker in coordinate assolute, animazioni spaziali su layer screen-space, binari vecchi, cache non invalidata | chiuso in R20l |
| R28 | card data duplicate (`6 dicembre 2011` + `2011`) | planner senza deduplica | chiuso |
| R31 | 106 frammenti da cache in una run "senza cache" | cache non bypassata per job | chiuso (`force_refresh`) |
| R34/R35 | upload stock respinto, errore `closed pipe` | risposta HTTP del Master non letta; token admin locale invece di quello Master | chiuso |
| **R36** | **HTTP 500 `m2m_quota_failure` — `source_range_out_of_bounds` su 3 segmenti, job `FAILED` dopo 369 s** | **finestre overlay oltre la durata dell'artifact; nessuna validazione locale nel binario in uso** | **corretto in questo ticket** |
| **R37 v1** | **HTTP 500 `m2m_quota_failure` — 24 segmenti `source_range_out_of_bounds`, job `job_1793825645902202468_f51650c7` `FAILED`** | **piano inviato senza `prepareFinalJobVideoSegments`; durate del piano confrontate con asset grezzi** | **corretto (preparer nel percorso)** |
| **R37 v2** | **`worker.reserveJobAssetProtection ... cached asset not found` su `002e0f…`, job `job_1793826115491415683_940f9d12` `FAILED`** | **`asset_key=sha256:<sha>` vs lease SHA nudo** | **corretto (chiave canonica)** |
| **R37 v3** | **`SUCCEEDED`; MP4 349.535.157 byte, SHA `93d729…`** | **preparer locale + chiavi canoniche** | **chiuso (decisione 2026-11-05: frammenti al Producer, film al remoto)** |

### R36 — dettaglio numerico

| segmento | `source_in_us` | `source_duration_us` | somma | durata dichiarata | sforamento |
| --- | --- | --- | --- | --- | --- |
| 94 | 14.000.000 | 1.013.000 | 15.013.000 | 15.000.000 | +13 ms |
| 125 | 20.000.000 | 6.645.000 | 26.645.000 | 26.625.000 | +20 ms |
| 195 | 24.000.000 | 1.213.000 | 25.213.000 | 25.209.000 | +4 ms |

Causa: il compilatore del piano usava `artifact_in_us` e `end_us - start_us` senza clamp sulla durata dell'artifact, e quando `duration_ms` mancava assumeva la durata dell'intera timeline, rendendo invisibile l'errore al validatore locale.

Correzione applicata:

1. `artifactDurationUS` entra nell'identità della finestra overlay.
2. La durata dell'artifact si ricava da `duration_ms`; se assente, dal frame grid certificato arrotondato per eccesso; se entrambi assenti il piano è rifiutato.
3. Le finestre sono tagliate alla durata dell'artifact (manifest e righe overlay dedicate).
4. Il composer riapplica il vincolo come difesa ulteriore.
5. `validateFinalJobPlanBindingRules` (già presente) viene eseguito prima dell'invio: un piano invalido muore in locale in meno di un secondo, non dopo sei minuti.

## Errori mascherati da correggere

- **`m2m_quota_failure` per un errore di validazione del piano.** Il codice è fuorviante: un piano invalido non è un problema di quota. Va restituito un errore dedicato (`invalid_compiled_render_plan`) con l'elenco dei segmenti. **Lato Producer chiuso:** l'envelope viene convertito in `PlanValidationError` terminale. **Lato Master 51 ancora aperto:** il server continua a restituire il codice fuorviante.
- **`closed pipe`** al posto dello status HTTP reale (r34). Risolto leggendo il body anche in errore.
- **`critical_path`** non è una sequenza: contiene intervalli sovrapposti. Ora è affiancato da `top_level_stage_intervals`; il vecchio campo resta per compatibilità.
- **Identità dei binari.** Due "fix inefficaci" erano worker/renderer vecchi in esecuzione (worker Go 16:35, renderer C++ non ricaricato). Ogni handoff deve verificare lo SHA del binario attivo, non solo l'health.
- **Identità della cache overlay.** La cache deve includere la versione dello schema/renderer; senza questo una correzione può essere mascherata da un MP4 riusato (r20b–r20k).

## Criteri di accettazione

- [x] **Stock metadata-only:** il Producer invia solo riferimento Drive + metadata salvati; nessun download, probe, hash, taglio, encode o upload stock prima del submit. Chiuso il 2026-11-07.
- [x] **Video finale solo remoto:** il Producer prepara i frammenti (stock incluso), l'audio e il piano; non crea, concatena o pubblica il film. Chiuso il 2026-11-05 con la rimozione del flusso locale e con i test di confine.
- [x] **Nessun workaround direct-source:** rimossi gate strict, preflight/allowlist host e boundary receipt. Chiuso il 2026-11-05.
- [x] MP4 finale r37 prodotto e verificato (SHA-256, size e durata coerenti con l'artefatto remoto).
- [x] Il piano compilato non contiene alcun segmento con `source_in_us + source_duration_us > asset.duration_us`.
- [x] Un piano che viola il vincolo è rifiutato in locale **prima** dell'invio, con segmento, valori e durata dell'asset.
- [x] Le finestre overlay sono tagliate alla durata certificata dell'artifact; nessun frame inventato.
- [x] Test di regressione con i tre intervalli reali di r36.
- [x] Il Producer rifiuta in locale il piano invalido e mappa l'envelope Master in `invalid_compiled_render_plan` terminale, senza retry.
- [ ] Il Master 51 restituisce nativamente `invalid_compiled_render_plan` invece di `m2m_quota_failure` (attività esterna al Producer).
- [x] Il report di run espone un solo owner: `RunReport.FinalJob` in `run_observability.report_json`; il checkpoint `workflow_payload_json` non è una seconda superficie di report.

## Non obiettivi

- Durata 20:00 esatta (dopo il taglio pause R31 misura 18:45,584): è una scelta editoriale, non un bug.
- Deploy standard bloccato da ICU 72 su host ICU 70 e migrazione 271: attrito di ambiente, tracciato a parte.

## 2026-11-07 — stock metadata-only e finestre whole-file (r39-r59)

Questa sezione prevale sulle precedenti. Il confine richiesto è: **nessun download, probe, hash, taglio, encode o upload di stock nel Producer prima del submit**. Il Producer risolve solo il riferimento Drive e i metadata già salvati dalla `stockpipeline`; il Master remoto scarica e processa lo stock. `prepareFinalJobVideoSegments` non chiama mai il preparer per `role=stock` e cancella `local_path`/`local_source_path`/`local_only` dal riferimento.

### Correzioni codice

- `finalJobStockPlanner.sceneClips`: niente più taglio parziale. Seleziona solo file interi; preferisce la durata esatta e altrimenti compone la scena con file interi che sommano esattamente alla durata canonica. Se non esiste una composizione, fallisce prima del submit. Cursore deterministico.
- `finalJobPlanBaseSegments`: rimossi `finalJobFrameCeilDurationUS` e il padding 4.979→5.000 ms. La durata stock è quella salvata; una scena stock di durata diversa dal file viene rifiutata.
- `alignFinalJobCompiledPlanAssetIDs`: legge anche `duration_ms` (i riferimenti stock non hanno `duration_us`); non allunga più l'asset alla finestra del piano. Una finestra oltre la durata salvata viene rifiutata.
- `prepareFinalJobVideoSegments`: controllo locale `source_in_us + source_duration_us <= asset.duration_us` prima di qualsiasi lavoro; stock pass-through invariato.
- Test aggiornati: planner whole-file, rifiuto scena 5 s su file 4.979 s, rifiuto finestra fuori bounds, accettazione file intero 4.979 s/119 frame, payload con file interi.

### Errori r39-r59

| Run | Errore | Stato 2026-11-07 |
| --- | --- | --- |
| r39/r39 retry | registry entry assente; worker cache `cached asset not found` | codice fail-fast; dati/runtime esterni |
| r40/r41/r43 | `manifest binding is not exact` | già corretto lato Producer (un asset per placement); verificare il Master |
| r42 | `m2m_auth_failure` / M2M key lookup | esterno |
| r44 | `signal: killed` | runtime |
| r45 | piano senza sorgente Drive | payload |
| r46/r47/r49 | 28119/28122, 119/120 | chiuso: durata esatta, niente padding, rifiuto locale del frame inventato |
| r50/r52 | `/run/chronon3d/chronon.sock` assente | runtime |
| r53-r55 | `source_range_out_of_bounds` 5.000.000 su 4.979.000 | chiuso: niente padding + `duration_ms` |
| r56 | `stock_window_not_packet_copyable` | codice Producer chiuso: nessuna finestra parziale. **Il vincolo whole-file del Master rende però incomponibili le durate TTS arbitrarie** (vedi r59b). |
| r58 | `output does not start with a keyframe` | **aperto lato Master**: una finestra stock parziale (es. base tagliata da un overlay) deve iniziare su un keyframe o essere ricodificata. Il Producer non può sondare/keyframare senza processare lo stock. |
| r59 | payload pronto, mai inviato | superato da r59b |
| r59b | `stock folder 1hqta… cannot fill the remaining 4624ms with whole files` | **risolto lato Producer**: scene stock allineate al multiplo del file (`end_ms=5000`) prima del compile audio; r59c supera il planner con 228 file interi. |
| r59c | `remote FINALIZE ... context deadline exceeded` (45 s) | **blocco Master**: il piano contiene 43 finestre stock parziali da overlay (r56/r58); FINALIZE in timeout e root Master 502. Nessun MP4. |

### r59b — retry con pool normalizzato (2026-11-07)

Job `job_1794033724475358199_2e277b9f`, payload `r59b-20261107-normalized.generate.json`, pool `1hqtaNHno5GHTlGyL9qPHwjn6OZExUD7N` (240 file, tutti 5000 ms). Script, traduzione e voiceover sono passati (audio finale 1.171.750 ms); il job si è fermato in `PUBLISHING_DOCUMENTS`:

```text
remote final job: stock folder 1hqtaNHno5GHTlGyL9qPHwjn6OZExUD7N
cannot fill the remaining 4624ms with whole files;
no partial stock windows are allowed
```

Il planner scomponeva ogni scena stock in file interi da 5000 ms; una scena con durata ≡ 4624 (mod 5000) lasciava una coda che nessun file intero poteva riempire.

**Correzione applicata (r59c):** `alignFinalJobStockSceneDurations` arrotonda ogni scena stock al multiplo della durata file dichiarata dal binding (`start_ms/end_ms` → `StockBinding.DurationMs`) prima del compile audio. La VO certificata mantiene la propria `source_duration_us`; il renderer riempie la coda con silenzio. Il binding r59c dichiara `end_ms=5000`; il planner ha composto 228 file interi da 5000 ms e il piano è passato con durata 1.184.970 ms. La correzione è coperta da test su proiezione audio, `ResolvedScenes` e timeline.

Il limite residuo non è più il multiplo di 5: nel piano r59c restano **43 segmenti stock parziali su 237** creati dagli overlay che sostituiscono una finestra della base stock (es. `segment-0002` 75 frame/3.125 s, `segment-0004` 13 frame/0.541 s su file da 5 s). Il Master rifiuta queste finestre (r56) o richiede keyframe/ricodifica (r58). Il Producer non può ridurle senza tagliare stock in locale (vietato) o rinunciare agli overlay. Serve una decisione Master:

1. **Master (raccomandato):** accettare una finestra parziale (`source_in_us=0` o offset keyframe) e ricodificarla/tagliarla. È l'unico punto che può trasformare byte stock senza violare il confine Producer.
2. **Stockpipeline:** produrre file interi che coincidano con le finestre overlay; richiede di processare stock dopo il TTS, contro il confine.

r59c ha superato il planner e ha creato il job Master `job_8fd5618188c7c918`; la FINALIZE è fallita con `context deadline exceeded` sul client a 45 s, mentre la radice del Master risponde 502. Il piano contiene le 43 finestre parziali sopra, quindi anche con un timeout più lungo la validazione Master va verificata.

Evidenza r59b: `ops/jobs/garlasco_editorial_e2e_20261030/r59b-20261107-normalized-job-full.json`, `r59b-20261107-normalized-poll2.log`, `r59b-20261107-normalized.done`. Evidenza r59c: `r59c-20261107-stock-aligned-job-full.json`, `r59c-20261107-stock-aligned-poll2.log`, `r59c-20261107-stock-aligned.done`, piano in `/tmp/final-job-debug/compiled-plan.json`. Nessun MP4 è stato prodotto; entrambi i job sono in dead letter queue con `retry_count=0`.

### r59d — misura locale senza Master (2026-11-07)

Stesso payload di r59c con `final_job=false` (job `job_1794037623992876736_67de02d2`, `SUCCEEDED`): misura la preparazione locale senza il confine remoto. Submit→termine 148 s; `created_at`→`updated_at` 132,1 s.

| Stage (critical path) | Tempo | Quota wall |
|---|---:|---:|
| `audio_compile` | 62,2 s | 47,1% |
| ├ `aac_encode` | 47,4 s | 35,9% |
| ├ `mix` | 7,3 s | 5,5% |
| `generate` (script LLM) | 43,6 s | 33,0% |
| `overlay_render` | 27,4 s | 20,7% |
| `prepare_join` (`semantic_wait`) | 24,2 s | 18,3% |
| `scene_analysis` | 6,7 s | 5,1% |
| sovrapposizione fra stage | 33,9 s | — |

Le due voci che dominano sono l'encode AAC del master (1.171,746 s di audio in 47,4 s, ~24,7× realtime) e la generazione dello script. `prepare_join` non è lavoro proprio: 24,2 s su 24,2 s sono `semantic_wait`, cioè l'attesa dei batch overlay contati in `overlay_render` (da cui la sovrapposizione).

r59c (`final_job=true`) sullo stesso payload: 232 s totali, di cui `remote_final_job` 111,8 s (52,8%) e stage locali `audio_compile` 51,5 s, `generate` 29,3 s, `prepare_join` 18,4 s, `overlay_render` 21,7 s. Il local-only toglie quindi ~80 s al confine remoto, non al lavoro locale.

Questa modalità **non produce video**: gli artefatti del run sono `script.json` e `final_audio.m4a` (AAC-LC 48 kHz stereo, 1.171,746 s, 28.660.686 byte, verificato con `ffprobe`). Nessun MP4 esiste, quindi nessun MP4 è verificato.

Evidenza r59d: `ops/jobs/garlasco_editorial_e2e_20261030/r59d-20261107-localonly.generate.json`, `r59d-20261107-localonly-run.out`, `r59d-20261107-localonly-submit-poll.log`, `r59d-20261107-localonly-job-full.json`, `r59d-20261107-localonly.done`.

### Residui esterni non chiudibili nel repo

- Master 51: accettazione/ricodifica delle finestre stock parziali (r58) e delle 43 finestre da overlay osservate in r59c. Il Producer può allineare le scene ai file interi, ma non può tagliare stock senza violare il confine; inoltre la FINALIZE r59c è andata in timeout a 45 s con root Master 502.
- Media registry: popolare le entry per ogni Drive file; altrimenti errore esplicito `run the stockpipeline first`.
- Worker cache: invalidare/ricostruire le entry mancanti.
- Chronon daemon: socket attivo prima del submit.
- M2M key/quota: health e autorizzazione del Master.

### Verifica eseguita

```text
go test ./internal/capabilities/scripts -run 'FinalJob' -count=1   PASS
go test ./internal/app/wiring -run 'FinalJob' -count=1             PASS
go test ./internal/capabilities/scripts -count=1                   PASS
```

La prima esecuzione della suite completa ha incontrato un flake di timing in `TestSceneTextReady_TTSDoesNotWaitForAnalysis` (non correlato); il test isolato è passato 3/3 e la suite completa è passata alla riesecuzione.

Retry r59b: servizio sano, job accettato e arrivato al voiceover, poi fallito nel planner stock come sopra. Il fail-fast ha evitato l'invio al Master di un piano incompleto.

Retry r59c: servizio ricostruito con l'allineamento (`build` 2026-11-07T06:54Z), planner superato con 228 file interi da 5000 ms e piano da 1.184.970 ms. Job Master `job_8fd5618188c7c918` creato; FINALIZE in timeout a 45 s, root Master 502. Nessun MP4 è stato prodotto o verificato.
