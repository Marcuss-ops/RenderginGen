# RG-ARCH-02 — Package `processor` con 46 file

- **Stato:** CLOSED (2026-11-24)
- **Priorità:** P3
- **Repo:** RenderingGen

## Evidenza

`renderinggen/internal/processor` contiene 46 file Go non-test. Non è al livello
di `overlay`, ma è il secondo package per superficie e concentra pipeline,
materializzazione, prescale, prepare e seam del compiler.

## Criteri di accettazione

- [x] Mappa delle responsabilità e delle dipendenze interne in
      `docs/evidence/code-hygiene-20261124/ARCH-02-MAP.md`.
- [x] Estratto `internal/processor/prescale` (sizing, resize, encode, cache,
      atomic write) con wrapper nel package root.
- [x] `go build ./...`, `go test ./internal/processor/...` e golden verdi;
      `TestPrescaleSubpackageIsIndependent` impedisce la dipendenza di ritorno.
