# Code hygiene RenderingGen — registro 2026-11-24

**Origine:** audit del 2026-11-24 su 329 file Go di produzione (~71.500 LOC).
**Regola:** un ticket per ogni classe di problema; nessuna cancellazione senza
riferimenti verificati e nessun criterio dichiarato verde senza evidenza.

## Ticket

| ID | Categoria | Priorità | Stato | Titolo |
|---|---|---|---|---|
| RG-HYG-01 | Pulizia | P2 | CLOSED | Directory `cmd/` vuote e inventario dei probe usa-e-getta |
| RG-HYG-02 | Dead code | P2 | CLOSED | Funzioni morte in `queue/internal/repository/postgres` |
| RG-HYG-03 | Dead code | P2 | CLOSED | Helper test-only nei file di produzione di `internal/overlay` |
| RG-HYG-04 | Pulizia | P2 | CLOSED | Binari, dump, media tracciati e font duplicato |
| RG-BUG-01 | Bug silenzioso | P1 | CLOSED | `jsonFacts`/`jsonProvenance` perdono l'errore di marshal |
| RG-BUG-02 | Errori ignorati | P2 | CLOSED | Inventario degli errori ignorati e casi che nascondevano fallimenti |
| RG-STATE-01 | Stato globale | P2 | CLOSED | Quattro singleton mutabili di processo |
| RG-DRY-01 | SSOT | P2 | CLOSED | `imageRecipeEffectID` e `resolveFeature` duplicati |
| RG-DRY-02 | DRY | P3 | CLOSED | `buildinfo`, `hashFile`, `copyBufPool`, `finite`, helper CLI |
| RG-ARCH-01 | Architettura | P2 | CLOSED | Package `overlay` con 90 file e hotspot >1.000 righe |
| RG-ARCH-02 | Architettura | P3 | CLOSED | Package `processor` con 46 file |
| RG-QUAL-01 | Complessità | P2 | CLOSED | Funzioni con CC 40–77 |
| RG-PANIC-01 | Robustezza | P3 | CLOSED | `panic` in produzione e busy-wait senza contesto |
| RG-LEGACY-01 | Legacy | P3 | CLOSED | Alias e fallback legacy sparsi |

## Ordine di esecuzione

1. Ondata 1 (zero rischio): RG-HYG-01..04.
2. Ondata 2 (bug e SSOT): RG-BUG-01, RG-BUG-02, RG-DRY-01, RG-STATE-01.
3. Ondata 3 (architettura): RG-ARCH-01/02, RG-QUAL-01, RG-PANIC-01,
   RG-LEGACY-01, RG-DRY-02.

## Gate comuni

`make test-gofmt`, `make test-module-standalone`, `make deadcode`,
`go vet ./...` e `make test-unit` in `RenderingGen`. Un ticket si chiude solo
con il gate eseguito sullo stesso working tree.

## Gate finale 2026-11-24

`make verify` in `RenderingGen` → **rc=0**: gofmt pulito, build standalone dei
tre moduli, deadcode con i soli export esterni allowlisted, `go vet` verde e
`make test-unit` verde su `renderinggen`, `queue` e `objectstore`.

## Terza passata 2026-11-24 (RG-DRY-02 … RG-LEGACY-01)

Chiusi RG-DRY-02, RG-ARCH-01, RG-ARCH-02, RG-QUAL-01, RG-PANIC-01 e
RG-LEGACY-01. Evidenze: `docs/evidence/code-hygiene-20261124/ARCH-01-MAP.md`,
`ARCH-02-MAP.md`, `PANIC-AUDIT.md`, `docs/operations/legacy-alias-census.md`.

- `make verify` → **rc=0** (gofmt, build standalone, deadcode, vet, test-unit
  su renderinggen/queue/objectstore).
- `go test -race ./internal/workspace ./internal/motion` → verde.
- `gocyclo -over 20` non riporta più `ValidateProbe`, `BuildWithFit`,
  `Config.validate`; restano solo `applyDefaults`, `readGOPPacketTable` e due
  test fuori scope.
- Guard aggiunti: parità buildinfo, singolo hash file, resolver CLI condivisi,
  `numeric.Finite`, limiti di file overlay, indipendenza prescale, panic
  allowlist, census alias.
