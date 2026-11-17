# TICKET — Workaround cross-repo da chiudere (2026-11-17)

Stato: aperto. Questi workaround sono deliberati e verificati, ma vivono in
RenderingGen solo perché il proprietario del contratto non li copre ancora.
Ogni sezione ha la prova nel codice e il criterio con cui si potrà eliminare.

## 1. ModernMap1: pre-clip delle route al posto del trim nativo

- **Evidenza:** `renderinggen/internal/overlay/semantic_map_modern.go`
  (`trimMapPolyline` e il commento "the native Vulkan shape path ignores an
  operator-level trim animation").
- **Cosa fa il worker:** taglia la polilinea proiettata in base a
  `trim_start`/`trim_end` prima di emettere il layer, perché il renderer
  disegna il range seed e ignora l'animazione di trim.
- **Proprietario:** Chronon3d (shape path trim).
- **Criterio di rimozione:** un render plan con `trim` animato produce il range
  richiesto su Vulkan; aggiungere un test di parità; poi eliminare
  `trimMapPolyline` e passare i valori al renderer.
- **Verifica:** piano ModernMap1 con route e trim 0.25..0.75, frame di
  controllo prima/dopo il taglio.

## 2. Audio: `transcode` richiesto, `copy` prodotto

- **Evidenza:** `renderinggen/internal/processor/render_selection.go`
  (`audioModeCopyOnly`, `warnInert`) e `prepare_stages.go`
  (`AudioInertParams`, warning).
- **Cosa fa il worker:** accetta `mode=transcode` e i parametri
  codec/sample_rate/channels, poi copia la traccia sorgente e registra un
  warning. L'artifact non corrisponde al contratto richiesto.
- **Proprietario:** PipelineGen/Chronon (contratto audio e mux).
- **Criterio di rimozione:** o il mux implementa il transcode, o il contratto
  rifiuta i modi non-copy in compilazione. Nel secondo caso aggiornare
  PipelineGen e aggiungere un test fail-closed.
- **Verifica:** piano con audio 44.1 kHz su sorgente 48 kHz e
  `mode=transcode`; `ffprobe` deve mostrare il sample rate richiesto, oppure il
  compile deve fallire con un errore esplicito.

## 3. Asset: URL dentro `logical_path`

- **Evidenza:** `renderinggen/internal/processor/materialize.go`
  (`assetSourceURL`, "LEGACY self-heal") e `asset_merge.go`.
- **Cosa fa il worker:** se `logical_path` è `http(s)`, lo tratta come
  `source_url`, scarica (hash verificato, cap 1 GiB) e logga. È un residuo del
  contratto precedente.
- **Proprietario:** PipelineGen (producer degli asset).
- **Criterio di rimozione:** nessun producer emette URL in `logical_path` per
  una finestra di migrazione concordata; poi il worker rifiuta lo schema e
  richiede `source_url`.
- **Verifica:** job di replay con manifest vecchio (deve continuare a funzionare
  fino alla data) e job nuovo senza `source_url` (deve fallire).

## 4. Minore: discovery dei build sibling

- **Evidenza:** `renderinggen/internal/tacticalfill/tacticalfill.go`
  (`../../ChrononTemplate/build/...`) e i comandi `date-animation` /
  `map-tactical-fill` (`../../Chronon3d/build/...`).
- **Cosa fa:** cerca un emitter/binario in path relativi alla CWD.
- **Criterio di rimozione:** spostare la discovery nei `cmd/` e richiedere un
  path esplicito nella libreria; i test possono passare un fake emitter.
