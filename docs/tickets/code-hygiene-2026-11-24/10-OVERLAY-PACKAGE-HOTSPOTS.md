# RG-ARCH-01 — Package `overlay` con 90 file e hotspot >1.000 righe

- **Stato:** CLOSED (2026-11-24)
- **Priorità:** P2
- **Repo:** RenderingGen

## Evidenza

`renderinggen/internal/overlay` è un unico package con 90 file Go non-test.
Hotspot:

| File | LOC | Churn (ultimi 150 commit) |
|---|---:|---:|
| `semantic_compile.go` | 531 | 54 |
| `semantic_map.go` | 1.097 | 23 |
| `semantic_map_modern.go` | 1.080 | alto |
| `semantic_image_compile.go` | 633 | 28 |
| `motion.go` | 642 | 26 |
| `text_spec.go` | 661 | 22 |
| `semantic_effects.go` | 860 | 15 |

## Vincolo

Il package è il cuore del lowering semantic overlay → render-plan.v2. Uno split
non è un move di file: `compileSemanticDecoded` e le fasi condividono tipi non
esportati e l'ordine delle fasi è contrattuale. Serve una certificazione golden
(`make golden-e2e` / fixture semantiche) prima e dopo.

## Criteri di accettazione

- [x] Inventario delle famiglie coese e proposta di 3–5 sottopackage con
      dipendenze acicliche: `docs/evidence/code-hygiene-20261124/ARCH-01-MAP.md`.
- [x] Estratto `internal/mapgeom` (`WrapDelta`, `WorldPoint`, `RectOverlap`,
      `TypographyScale`) con alias nel package root e nessun cambiamento di piano.
- [x] Golden semantiche e `go test ./internal/overlay/...` verdi; nessun file
      golden modificato dallo split.
- [x] Nessun file non-test oltre 800 righe nel package root; il guard
      `TestOverlayRootFilesStayUnder800` lo impone.
