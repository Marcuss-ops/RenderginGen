# Census alias legacy — RG-LEGACY-01

Data: 2026-11-24. Ambito: tabelle di alias in codice di produzione
RenderingGen. Ogni tabella ha un owner, una data di ritiro e un test che la
esercita. Una nuova tabella senza questa registrazione fallisce
`TestLegacyAliasTablesAreCensused`.

| Tabella | File | Owner | Ritiro pianificato | Test |
|---|---|---|---|---|
| `legacyTemplateAliases` | `renderinggen/internal/overlay/registry.go` | overlay / template registry | 2027-03-31 | `TestLegacyTemplateAliasesResolveToRegisteredTemplates` (`registry_test.go`) |
| `legacyImageCompositionAliases` | `renderinggen/internal/overlay/image_composition.go` | overlay / image composition | 2027-06-30 | `TestLegacyImageCompositionAliasesResolveToCounts` (`image_composition_alias_test.go`) |
| `entityStyleAliases` | `renderinggen/internal/overlay/entity_style.go` | overlay / entity styles | 2027-06-30 | `TestEntityStyleAliasWarningStateIsBounded` (`entity_style_registry_test.go`) |
| `documentaryPhraseAliases` | `renderinggen/internal/overlay/documentary_phrase_motion.go` | overlay / documentary motions | 2027-03-31 | `TestCatalogPhraseHighlightAliasesLowerTheirDocumentaryRecipes` |
| `envAliases` | `renderinggen/internal/config/config_env.go` | RenderingGen config | 2027-03-31 | `TestLegacyEnvironmentAliasesStillApply` (`config_verification_test.go`) |

## Regole

1. Il nome canonico vince sempre sull'alias; l'alias è un fallback di lettura.
2. Un alias non si aggiunge senza un test che lo risolva e senza una riga in
   questa tabella.
3. Alla data di ritiro la tabella va rimossa insieme al test che la esercita;
   se un artefatto sealed esterno la richiede ancora, la riga va aggiornata con
   owner e nuova data, non lasciata scadere in silenzio.

## Percorso composite legacy

`compileLegacyCompositeImageLayers` e il marker `legacy_composite_replay` sono
stati rimossi il **2026-11-24**, in anticipo sulla deadline del 2026-12-31,
come registrato in `docs/tickets/TICKET-LEGACY-COMPOSITE-REPLAY-REMOVAL-2026-12-31.md`.
Il census 2026-11-24 di questo checkout trovava **0** piani di produzione con
`image_layers` non vuoto; un piano che porta ancora il marker viene ora
rifiutato dal decoder con `unknown field "legacy_composite_replay"`.
