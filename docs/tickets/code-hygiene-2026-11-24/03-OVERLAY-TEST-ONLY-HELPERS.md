# RG-HYG-03 — Helper test-only nei file di produzione di `internal/overlay`

- **Stato:** CLOSED (2026-11-24) — i sette helper sono in `zz_test_only_helpers_test.go`; il package overlay compila e i test passano.
- **Priorità:** P2
- **Repo:** RenderingGen (`renderinggen` module)

## Evidenza

Queste funzioni non hanno chiamanti di produzione; i riferimenti sono solo in
`*_test.go` dello stesso package:

| Funzione | File di produzione |
|---|---|
| `isTextLikeKind` | `animation_policy.go:66` |
| `animationUsesMultiImageRecipe` | `animation_policy.go:71` |
| `autoFitEntityPortrait` | `image_autofit.go:124` |
| `resolveMotion` | `motion.go:227` (verificare prima del move) |
| `compileSemantic` | `semantic_compile.go:85` |
| `animationForMotion` | `semantic_helpers.go:18` |
| `animationForDefinition` | `semantic_helpers.go:313` |

`compileSemanticDecoded` e `animationForMotionTarget` sono invece usati dalla
produzione e non vanno spostati.

## Criteri di accettazione

- [x] Ogni helper elencato vive in `zz_test_only_helpers_test.go`, o è stato
      eliminato se il test era l'unico uso.
- [x] Nessuna funzione di produzione viene rimossa per errore: verifica `rg`
      su tutto il modulo, non solo sul package.
- [x] `go build ./...`, `go vet ./...`, `go test ./internal/overlay/...` e
      `make deadcode` verdi (2026-11-24).
