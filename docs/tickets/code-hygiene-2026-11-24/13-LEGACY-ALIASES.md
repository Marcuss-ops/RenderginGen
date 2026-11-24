# RG-LEGACY-01 — Alias e fallback legacy sparsi

- **Stato:** CLOSED (2026-11-24)
- **Priorità:** P3
- **Repo:** RenderingGen

## Evidenza

172 occorrenze di `legacy`/`deprecated`/`fallback`. Cinque tabelle di alias in
file diversi (`legacyTemplateAliases`, `legacyImageCompositionAliases`,
`entityStyleAliases`, `documentaryPhraseAliases`, `envAliases`). Il percorso
`compileLegacyCompositeImageLayers` aveva 0 piani nel repo; il percorso e il
marker sono stati rimossi il 2026-11-24 da
`docs/tickets/TICKET-LEGACY-COMPOSITE-REPLAY-REMOVAL-2026-12-31.md`.

## Criteri di accettazione

- [x] Le cinque tabelle hanno owner e data di ritiro in
      `docs/operations/legacy-alias-census.md`.
- [x] Il ticket composite è aggiornato con il census 2026-11-24: 0 piani di
      produzione; path, marker, metriche e schema rimossi il 2026-11-24.
- [x] `TestLegacyAliasTablesAreCensused` fallisce su una nuova tabella non
      registrata; ogni tabella esistente ha un test che la esercita.
