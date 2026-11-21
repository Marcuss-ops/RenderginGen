# TICKET — Workaround cross-repo da chiudere (2026-11-17)

Stato: **aperto** (riverificato 2026-11-21). Tre workaround restano deliberati e
verificati; il quarto (discovery dei build sibling) è stato ritirato il
2026-11-21. I tre residui vivono in RenderingGen solo perché il proprietario del
contratto non li copre ancora. Ogni sezione ha la prova nel codice e il criterio
con cui si potrà eliminare.

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

## 4. Minore: discovery dei build sibling — RISOLTO 2026-11-21

- **Evidenza:** `renderinggen/internal/tacticalfill/tacticalfill.go`
  (`../../ChrononTemplate/build/...`) e i comandi `date-animation` /
  `map-tactical-fill` (`../../Chronon3d/build/...`).
- **Cosa faceva:** cercava un emitter/binario in path relativi alla CWD.
- **Rimozione eseguita:** `tacticalfill.ResolveEmitter` ora restituisce solo il
  path esplicito o `CHRONONTEMPLATE_EMIT_TACTICAL_FILL`; la discovery dei build
  sibling vive in `cmd/map-tactical-fill` (`resolveEmitter`). `date-animation`
  ed `editorial-map` avevano già la discovery nel proprio `cmd/`.
- **Verifica:** `go build ./...`, `go test ./internal/tacticalfill
  ./cmd/map-tactical-fill -count=1` verdi; la libreria non contiene più path
  relativi `../../ChrononTemplate/build/...`.

## Verifica 2026-11-21 — tre workaround restano, il quarto è ritirato

Ricontrollo sul tree `RenderingGen@c515fb5` del 2026-11-21: i primi tre punti
sono ancora presenti nel codice e nessun proprietario ha chiuso il criterio di
rimozione. La discovery dei build sibling (#4) è stata spostata nei `cmd/` e
la libreria non la contiene più. Il working tree ha modifiche concorrenti su
`config.yaml` e `internal/overlay/chain_segments.go`; nessuna tocca i simboli
qui sotto.

| # | Simbolo cercato | Esito |
|---|---|---|
| 1 | `trimMapPolyline` (`internal/overlay/semantic_map_modern.go:263`, definizione in `semantic_map_modern_geometry.go:19`) | presente |
| 2 | `audioModeCopyOnly` (`internal/processor/render_selection.go:78`, usato in `prepare_stages.go:432`) | presente |
| 3 | `assetSourceURL` (`internal/processor/materialize.go:221`, chiamato a `:128`) | presente |
| 4 | discovery `../../ChrononTemplate/build/...` | **rimosso dalla libreria**; vive in `cmd/map-tactical-fill` |

Il ticket resta quindi **aperto** per i punti 1–3: non sono residui di
RenderingGen da ripulire, sono dipendenze dal contratto a monte. La chiusura
richiede, in ordine: (1) trim nativo del path shape su Vulkan con test di
parità; (2) mux che onora `transcode` oppure contratto che lo rifiuta in
compilazione; (3) nessun producer che emette URL in `logical_path` per una
finestra di migrazione concordata.
