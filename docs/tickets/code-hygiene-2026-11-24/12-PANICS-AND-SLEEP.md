# RG-PANIC-01 — `panic` in produzione e busy-wait senza contesto

- **Stato:** CLOSED (2026-11-24)
- **Priorità:** P3
- **Repo:** RenderingGen

## Evidenza

- 9 `panic(...)` in codice di produzione, tra cui
  `internal/motion/catalog.go:388-409`,
  `internal/overlay/geographic_tag_styles.go:20-28`,
  `internal/overlay/maps_renderer_catalog.go:64`,
  `internal/vramprobe/probe.go:307`.
- `time.Sleep(10 * time.Millisecond)` in
  `internal/workspace/workspace_lock_other.go:41`, senza `context.Context`.

## Criteri di accettazione

- [x] Ogni `panic` è classificato in
      `docs/evidence/code-hygiene-20261124/PANIC-AUDIT.md`; `vramprobe.mustJSON`
      è stato sostituito da un errore propagato.
- [x] Gli otto panic residui sono invarianti di catalogo embedded, documentati
      e esercitati da `TestCanonicalCatalogInitializationRegistersMotions` e
      `TestEmbeddedCatalogsInitialize`.
- [x] Il fallback lock usa backoff esponenziale con tetto 250 ms e deadline
      30 s; `TestWorkspaceLockBackoffIsBounded` pinna il limite.
- [x] `go test -race ./internal/workspace/... ./internal/motion/...` verde.
