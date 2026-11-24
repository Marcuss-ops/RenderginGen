# ARCH-01 — overlay inventory e split

Data: 2026-11-24. Baseline: 90 file Go non-test, 54.870 righe; hotspot
`semantic_map.go` 1.097, `semantic_map_modern.go` 1.080,
`semantic_effects.go` 860.

## Famiglie coese

| Famiglia | File rappresentativi | Responsabilità |
|---|---|---|
| Admission e contratto | `semantic_contract_types.go`, `semantic_compile.go`, `semantic_plan_pipeline.go` | validazione del piano, ordine delle fasi, stats |
| Map lowering | `semantic_map.go`, `semantic_map_layers.go`, `semantic_map_modern.go`, `semantic_map_modern_validation.go` | basemap, pin, label, ModernMap1 |
| Testo | `text_spec.go`, `text_background_plate.go`, `text_plate_style.go` | tipografia, plate, fit |
| Entity/image | `semantic_entity_compile.go`, `semantic_image_compile.go`, `entity_style.go` | card, ricette immagine, alias |
| Motion | `motion.go`, `animation_group_compile.go`, `motion_admission.go` | vocabolario e lowering motion |
| Style/runtime | `runtime_style.go`, `selection_model.go`, `plan_model.go` | stile runtime e modello di piano |
| Cataloghi | `official_presets.go`, `maps_renderer_catalog.go`, `geographic_tag_styles.go` | vocabolari embedded |

## Sottopackage proposti (dipendenze acicliche)

1. **`internal/mapgeom` — estratto ora.** Geometria piana pura: wrap
   antimeridiano, proiezione relativa, overlap di rettangoli, scala tipografica.
   Importa solo `geo` e `math`; `overlay` importa `mapgeom`, mai il contrario.
2. `overlay/mapcatalog` (futuro): caricamento e validazione dei cataloghi map.
   Dipenderebbe solo da `encoding/json` e dagli asset embedded.
3. `overlay/textspec` (futuro): specifica testo e geometria plate. Non deve
   importare `overlay` root; i tipi condivisi vanno prima estratti.
4. `overlay/entitystyle` (futuro): tabella stili e alias. Dipenderebbe da
   `numeric`/`motion`, non dal compiler.
5. `overlay/runtime` (futuro): modello di piano e selezione runtime.

L'ordine è quello delle dipendenze: `mapgeom` è già foglia; gli altri si possono
estrarre solo dopo aver spostato i tipi condivisi, altrimenti si crea un ciclo
`overlay -> subpackage -> overlay`.

## Estratto effettivo

`internal/mapgeom/overlay_geometry.go` esporta `WrapDelta`, `WorldPoint`,
`RectOverlap`, `TypographyScale`. Nel package root restano alias con gli stessi
nomi (`wrapMapPlaneDelta`, `mapWorldPoint`, `mapLabelRectsOverlap`,
`mapTypographyScale`), così nessun call-site cambia.

## Split dei file

| Prima | Dopo |
|---|---|
| `semantic_map.go` 1.097 | `semantic_map.go` 727 + `semantic_map_layers.go` 370 |
| `semantic_map_modern.go` 1.080 | `semantic_map_modern.go` 409 + `semantic_map_modern_validation.go` 681 |
| `semantic_effects.go` 860 | `semantic_effects.go` 686 + `semantic_effects_validation.go` 180 |

Nessun file non-test del package root supera 800 righe; il guard
`TestOverlayRootFilesStayUnder800` lo impone.

## Verifica

- `go test ./internal/mapgeom ./internal/overlay` → verde.
- Nessun file golden modificato: lo split è un move di dichiarazioni, non un
  cambio di piano.
- `go test ./internal/overlay` esegue le suite golden già presenti.
