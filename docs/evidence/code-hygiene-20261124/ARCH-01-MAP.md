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
2. `overlay/mapcatalog` — **estratto il 2026-11-25.** Caricamento e
   validazione del catalogo map renderer; dipende solo dalla standard library e
   dagli asset embedded.
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

## Aggiornamento 2026-11-25 — mapcatalog e corpus ermetico

- Estratto `internal/overlay/mapcatalog` (`catalog.go`): il root conserva solo
  gli alias `chrononPluginMaps`, `certifiedMap*` e `loadChrononPluginMapsCatalog`.
  Il sottopackage è standard-library-only; `TestMapcatalogSubpackageIsIndependent`
  impedisce un import di ritorno verso `overlay`.
- Il corpus overlay è stato vendored sotto
  `RenderingGen/contracts/overlay-v2/`: `TestOverlayContractCorpus` ora è
  ermetico e gira nel checkout standalone. `TestContractCorpusMatchesWorkspaceSources`
  confronta byte e digest con il corpus del workspace quando i sibling sono
  presenti, e skippa nel CI standalone.
- Il digest `short_phrase_text` è stato riallineato a
  `d89efe750f8adb1378f8fb4409931ab78e82e84990a1146f0f0f404caecfb979`
  dopo il refresh del fixture producer `doc_14_dual_highlight_sequence`.
- `template_alias_lowercase` esenta i fixture vendored: sono dati wire del
  producer, non codice, e contengono legittimamente `gpe_default`.
- Verifica: build, vet, `go test ./internal/overlay/mapcatalog`, `go test ./internal/overlay`,
  `go test ./internal/architecture` e `contracts/overlay-v2/verify.sh` verdi; il dettaglio
  è in `ARCH-03-MAPCATALOG-CORPUS.md`.
