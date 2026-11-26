# ARCH-03 — mapcatalog leaf e corpus overlay ermetico

Data: 2026-11-25. Baseline: RenderingGen `2b5630f`, working tree pulito.

## Contesto

Il primo passo della roadmap ARCH-01 era `overlay/mapcatalog`. In parallelo,
`TestOverlayContractCorpus` era rosso per due motivi distinti:

1. il fixture producer `short_phrase_text.plan.json` era cambiato a
   `doc_14_dual_highlight_sequence`, ma il manifest del corpus pinna ancora il
   digest precedente;
2. il test leggeva `contracts/overlay-v2/manifest.json` quattro livelli sopra
   il package, quindi in un checkout RenderingGen standalone il file non
   esisteva e il test falliva prima ancora del confronto.

## Modifiche

### `internal/overlay/mapcatalog`

- `catalog.go` possiede `Catalog`, `Embedded`, `Load`, `Set` e `FileName`.
- L'asset embedded vive in
  `internal/overlay/mapcatalog/catalog/chrononplugin_maps_renderer_catalog.v1.json`.
- Il root `maps_renderer_catalog.go` è una compatibility layer di poche righe:
  `chrononPluginMaps`, `certifiedMapCompositions`,
  `certifiedMapCameraAnimations`, `certifiedMapLabelAnimations` e
  `loadChrononPluginMapsCatalog`.
- `TestMapcatalogSubpackageIsIndependent` fallisce se il sottopackage importa
  un package interno RenderingGen: deve restare standard-library-only.
- `catalog_test.go` copre `Load` (catalogo valido e 11 varianti invalide:
  campo sconosciuto, JSON trailing, schema/versione/source/renderer errati,
  `base_flyover` non vuoto, liste vuote, id blank o duplicati), `Set` e la
  validità del catalogo embedded.

### Corpus overlay

- `RenderingGen/contracts/overlay-v2/` contiene il manifest, i 7 fixture
  PipelineGen e i fixture sintetici/BrainRust.
- `TestOverlayContractCorpus` usa la root del repo (`../../..`) e il manifest
  vendored: è ermetico nel CI standalone.
- `TestContractCorpusMatchesWorkspaceSources` confronta i byte dei fixture e i
  digest con il corpus del workspace; skippa quando `refactored/` o il manifest
  del workspace non sono presenti.
- Il digest `short_phrase_text` è ora
  `d89efe750f8adb1378f8fb4409931ab78e82e84990a1146f0f0f404caecfb979`.
- `template_alias_lowercase` esenta `contracts/overlay-v2/fixtures/`: i fixture
  producer contengono legittimamente gli alias live `gpe_default`; non sono
  codice e non devono essere riscritti, altrimenti il corpus non rappresenta
  più il wire reale.

## Verifica

| Comando | Esito |
|---|---|
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test ./internal/overlay/mapcatalog -count=1` | ok |
| `go test ./internal/overlay -run 'TestOverlayContractCorpus|TestContractCorpusMatchesWorkspaceSources' -count=1` | ok |
| `go test ./internal/overlay -count=1` | ok |
| `go test ./internal/architecture -count=1` | ok |
| `bash contracts/overlay-v2/verify.sh` | exit 0 (BrainRust 34/34, PipelineGen e RenderingGen ok) |

## Guardrail

- Nessun cambio al lowering: i digest dei fixture diversi da `short_phrase_text`
  sono rimasti invariati.
- Non è stato modificato il decoder stretto per far passare un fixture.
- Il corpus del workspace resta la fonte di verità; il vendored è uno snapshot
  verificato, non una seconda authority silenziosa.
