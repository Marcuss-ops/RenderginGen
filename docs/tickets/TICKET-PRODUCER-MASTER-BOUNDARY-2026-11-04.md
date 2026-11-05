# TICKET — Confine Producer 77 / Master 51 e piano finale immutabile

- **Data:** 2026-11-04
- **Componenti:** PipelineGen (Producer 77), RenderingGen (overlay), Master 51 (worker remoto)
- **Contesto:** job editoriale Garlasco da 20 minuti, run r10–r37
- **Stato:** r37 chiuso con MP4 verificato (2026-11-04 21:16). **Debito architetturale aperto:** il workaround prepara 265 frammenti in locale, quindi il confine "no frammenti / stock sola lettura" non è ancora rispettato.

## Regola di perimetro

Lo stock è **sola lettura**. Il Producer non deve:

- caricare file stock sul Master (`POST /api/v1/creator/assets`);
- ricodificare finestre stock in locale;
- produrre frammenti video da inviare al worker remoto.

Il Producer produce **solo** ciò che è suo: render overlay, TTS/voce, audio finale certificato e **un unico piano immutabile** (`compiled_render_plan_json` + SHA-256). Lo stock resta un riferimento sorgente che il Master risolve e assembla.

Questa regola è stata violata più volte nella sessione (r34–r36: ~98 asset e ~320 MB di stock trasferiti, poi rimossi) e va considerata un vincolo assoluto, non un'ottimizzazione.

Residuo noto: la risoluzione stock resta in sola lettura (download Drive, probe, SHA) perché il piano deve dichiarare durata e identità della sorgente. L'esposizione su objectstore è l'ultimo passaggio da eliminare quando il Master 51 risolverà direttamente l'ID Drive. Con r37 questo residuo è diventato una **violazione effettiva**: il preparer locale ha prodotto e pubblicato 265 frammenti (237 stock, 25 overlay, 3 clip).

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

### Debito architetturale aperto (importante)

Il MP4 è consegnato, ma per farlo passare dal worker copy-only è stato **riattivato il preparer locale**: 265 frammenti video (237 stock, 25 overlay, 3 clip) sono stati tagliati e pubblicati su objectstore. Questo **viola** la regola "il Producer non produce frammenti / lo stock è sola lettura" e il criterio "nessun upload stock/frammento". La chiusura del confine richiede che il Master/worker accettino lo slicing diretto delle sorgenti (o che il piano usi solo overlay già pronti); finché non accade, il workaround è necessario per ottenere un MP4 con l'attuale executor `video.assemble.copy.v1`.

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
| **R37 v3** | **`SUCCEEDED`; MP4 349.535.157 byte, SHA `93d729…`** | **preparer locale + chiavi canoniche** | **chiuso con debito** |

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

- **`m2m_quota_failure` per un errore di validazione del piano.** Il codice è fuorviante: un piano invalido non è un problema di quota. Va restituito un errore dedicato (`invalid_compiled_render_plan`) con l'elenco dei segmenti.
- **`closed pipe`** al posto dello status HTTP reale (r34). Risolto leggendo il body anche in errore.
- **`critical_path`** non è una sequenza: contiene intervalli sovrapposti. Ora è affiancato da `top_level_stage_intervals`; il vecchio campo resta per compatibilità.
- **Identità dei binari.** Due "fix inefficaci" erano worker/renderer vecchi in esecuzione (worker Go 16:35, renderer C++ non ricaricato). Ogni handoff deve verificare lo SHA del binario attivo, non solo l'health.
- **Identità della cache overlay.** La cache deve includere la versione dello schema/renderer; senza questo una correzione può essere mascherata da un MP4 riusato (r20b–r20k).

## Criteri di accettazione

- [ ] Nessun upload stock/frammento verso il Master nel percorso finale. **Riaperto dal workaround r37:** `BuildFinalJobPayloadsWithSegmentPreparer` prepara e pubblica 265 frammenti; il MP4 è prodotto, ma il confine non è rispettato.
- [x] MP4 finale r37 prodotto e verificato (SHA-256, size e durata coerenti con l'artefatto remoto).
- [x] Il piano compilato non contiene alcun segmento con `source_in_us + source_duration_us > asset.duration_us`.
- [x] Un piano che viola il vincolo è rifiutato in locale **prima** dell'invio, con segmento, valori e durata dell'asset.
- [x] Le finestre overlay sono tagliate alla durata certificata dell'artifact; nessun frame inventato.
- [x] Test di regressione con i tre intervalli reali di r36.
- [ ] Il ticket `m2m_quota_failure` viene sostituito da un errore di validazione dedicato lato Master (attività del Master 51, non del Producer).
- [ ] Il report di run espone un solo owner (`run_observability.report_json`).

## Non obiettivi

- Durata 20:00 esatta (dopo il taglio pause R31 misura 18:45,584): è una scelta editoriale, non un bug.
- Deploy standard bloccato da ICU 72 su host ICU 70 e migrazione 271: attrito di ambiente, tracciato a parte.
