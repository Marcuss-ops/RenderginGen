# PANIC-AUDIT — RG-PANIC-01

Data: 2026-11-24. Ambito: codice di produzione Go di RenderingGen, queue e
objectstore (`--glob '!**/*_test.go'`).

## Criterio

Ogni `panic(` è una delle due categorie:

- **INIT** — invariante di inizializzazione su dati embedded, non raggiungibile
  da input esterno. Il messaggio nomina il catalogo e un test di package prova
  che il catalogo si carichi.
- **REMOVED** — il panic non era un invariante ed è stato sostituito da un errore
  propagato.

## Inventario

| Sito | Categoria | Motivo | Test |
|---|---|---|---|
| `internal/motion/catalog.go:388` | INIT | `Canonical()` non riesce a leggere il catalogo embedded | `TestCanonicalCatalogInitializationRegistersMotions` |
| `internal/motion/catalog.go:392` | INIT | un motion canonico non si registra | idem |
| `internal/motion/catalog.go:404` | INIT | la retirement metadata non si applica | idem |
| `internal/motion/catalog.go:409` | INIT | gli short-phrase styles non si registrano | idem |
| `internal/overlay/maps_renderer_catalog.go:64` | INIT | il catalogo ChrononPlugin embedded non decodifica | `TestEmbeddedCatalogsInitialize` + `TestChrononPluginMapsCatalogOwnsTheMapVocabulary` |
| `internal/overlay/geographic_tag_styles.go:20` | INIT | il catalogo embedded degli stili geografici non decodifica | `TestEmbeddedCatalogsInitialize` |
| `internal/overlay/geographic_tag_styles.go:25` | INIT | preset senza nome nel catalogo embedded | idem |
| `internal/overlay/geographic_tag_styles.go:28` | INIT | preset duplicato nel catalogo embedded | idem |
| `internal/vramprobe/probe.go:307` (ex `mustJSON`) | REMOVED | `json.Marshal` di una stringa non richiede un panic: l'errore ora è propagato a `BuildProbePlan` | `go test ./internal/vramprobe` |

## Busy-wait

`internal/workspace/workspace_lock_other.go` non ha un `flock` portabile. Il
fallback usa ora un backoff esponenziale con tetto a 250 ms e una deadline di
30 s; oltre la deadline restituisce un errore invece di attendere per sempre.
Il test `TestWorkspaceLockBackoffIsBounded` pinna il tetto e la deadline.

I `time.Sleep` in `internal/vramprobe/pool.go` sono intervalli di campionamento
GPU, non attese su lock, e non rientrano in questo ticket.
