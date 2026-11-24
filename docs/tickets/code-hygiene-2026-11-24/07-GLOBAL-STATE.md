# RG-STATE-01 — Quattro singleton mutabili di processo

- **Stato:** CLOSED (2026-11-24) — l'audit era stale su `prefetchInflight`: oggi è un campo per-Processor, non un globale. Restano tre singleton intenzionali documentati e pinnati da test.
- **Priorità:** P2
- **Repo:** RenderingGen (`renderinggen` + `queue`)

## Evidenza e triage

| Simbolo | File | Valutazione |
|---|---|---|
| `prefetchInflight` | `renderinggen/internal/processor/processor.go:59` + `pipeline.go:200` | **Già risolto:** è un campo per-Processor con `prefetchMaxInflight = 4` e init lazy in `prefetchSlots()`. L'audit citava il vecchio globale. |
| `entityStyleAliasWarned` | `renderinggen/internal/overlay/entity_style.go:394` | `LoadOrStore` avviene solo dopo il lookup in `entityStyleAliases`, quindi le chiavi sono un insieme finito; il rischio "unbounded" dell'audit non è reale. |
| `configuredBinaries` | `renderinggen/internal/media/binaries.go:38` | Configurazione di processo per ffmpeg/ffprobe; `atomic.Value` con replace wholesale. |
| `emptyJobTypeTotal` | `queue/internal/repository/postgres/repository.go:38` | Contatore globale; la funzione `EmptyJobTypeNormalizedTotal` è usata dai test. |

## Esito (2026-11-24)

- `prefetchInflight` è per-Processor (campo, non globale) e il cap 4 è
  pinnato da `TestPrefetchInflightCapIsFour`, che verifica anche l'init lazy
  dello zero-value `Processor`.
- `entityStyleAliasWarned` resta, ma il test
  `TestEntityStyleAliasWarnedOnlyTracksCatalogAliases` dimostra che un selettore
  sconosciuto non cresce la mappa: le chiavi sono solo quelle della tabella
  finita `entityStyleAliases`.
- `configuredBinaries` è la configurazione ffmpeg/ffprobe di processo, letta
  atomicamente e sostituita in blocco; non è un leak.
- `emptyJobTypeTotal` parte da una baseline azzerata esplicitamente
  (`Swap(0)`) e viene ripristinato con `t.Cleanup`, quindi il delta `+1` non
  dipende dall'ordine di esecuzione.

## Criteri di accettazione

- [x] Per i tre singleton intenzionali il codice contiene il motivo e un test
      che ne pinna il comportamento.
- [x] `prefetchInflight` è per-Processor con default 4, pinnato dal test.
- [x] `emptyJobTypeTotal` ha una baseline azzerata esplicitamente e ripristinata
      dal test, senza dipendere dall'ordine di esecuzione.
- [x] I test mirati di overlay e processor passano.
