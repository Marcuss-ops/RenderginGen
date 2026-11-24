# RG-QUAL-01 — Funzioni con complessità ciclomatica 40–77

- **Stato:** CLOSED (2026-11-24) — tutte e cinque le funzioni sono state scomposte.
- **Priorità:** P2
- **Repo:** RenderingGen

## Evidenza

| Funzione | File | CC | LOC |
|---|---|---:|---:|
| `validate` | `renderinggen/internal/schemaval/schemaval.go:279` | 77 | 199 |
| `ValidateProbe` | `renderinggen/internal/media/profile.go:382` | 59 | 79 |
| `BuildWithFit` | `renderinggen/internal/tacticalfill/tacticalfill.go:111` | 45 | 117 |
| `(*Config).validate` | `renderinggen/internal/config/config_validate.go:16` | 40 | 112 |
| `ValidateJobBatch` | `queue/internal/model/model.go:144` | 40 | 100 |

## Criteri di accettazione

- [x] `ValidateJobBatch` è scesa sotto CC 20 estraendo
      `validatePreparedRenderFamily`; i test esistenti della famiglia prepared
      restano verdi.
- [x] `schemaval.validate` è scesa sotto CC 20 estraendo
      `validateSchemaKeywords`, `validateObjectKeywords`,
      `validateArrayKeywords`, `validateStringKeywords` e
      `validateNumberKeywords`; `go test ./internal/schemaval` verde.
- [x] Le altre tre funzioni (`ValidateProbe`, `BuildWithFit`,
      `Config.validate`) scendono sotto CC 20 senza cambiare messaggi/ordine
      degli errori osservabili. Evidenza: `gocyclo -over 20` non le riporta più;
      restano solo `applyDefaults` (26), `readGOPPacketTable` (26) e due test
      fuori dallo scope del ticket.
- [x] I test esistenti restano verdi; `schemaval` ha già i casi di regressione
      sui rami estratti.
- [x] `go vet` e test dei moduli `renderinggen` e `queue` verdi (2026-11-24).
