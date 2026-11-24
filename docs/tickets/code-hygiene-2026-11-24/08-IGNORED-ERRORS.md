# RG-BUG-02 — Errori ignorati con `_ = fn(...)`

- **Stato:** CLOSED (2026-11-24) — inventario completo; 43 siti non più ignorati
  inline, 66 residui best-effort documentati, nessun sito (b) residuo.
- **Priorità:** P2
- **Repo:** RenderingGen

## Evidenza

L'audit citava 108 chiamate con risultato scartato: 44 in
`renderinggen/internal/workspace/workspace.go`, 7 in
`processor/prescale_images.go`, 5 in `storage/storage.go`.

Il comando riproducibile
`rg -n '^\s*_ =' renderinggen queue objectstore --glob '*.go' --glob '!*_test.go'`
ne trova **109** all'inizio di questo passaggio: la differenza è una riga di
produzione aggiunta dopo lo snapshot dell'audit. I 62 siti nei file `_test.go`
sono cleanup di fixture e restano fuori dall'inventario di produzione.

## Esito

- **Inventario completo:** [`docs/evidence/code-hygiene-20261124/IGNORED-ERRORS-INVENTORY.md`](../../evidence/code-hygiene-20261124/IGNORED-ERRORS-INVENTORY.md).
- **43 siti chiusi:** 16 ora loggano il fallimento (health, queue server,
  objectstore server, worker_pools, plan_dump, l2_write, chainpancheck), 3 sono
  stati rimossi (riferimento morto in `semantic_text`, pre-remove ridondanti in
  `canonical_encode`) e 24 sono stati consolidati o propagati in `workspace`.
- **66 siti residui:** tutti classificati (a) con motivazione nel codice o nel
  documento; nessun sito (b) residuo.
- **Bug collaterale corretto:** `materialize.go` passava `ws.Root()` come job ID
  a `dumpCompiledPlan`; ora passa `job.ID`, quindi il dump finisce nel file che
  il diagnostic promette.
- **Nessuna regressione:** i test mirati di workspace, storage, processor, media,
  health, geofeatures, overlay, tacticalfill e queue passano, inclusi i race
  detector sui package toccati.

## Criteri di accettazione

- [x] Inventario dei siti classificato in: (a) best-effort con commento,
      (b) errore da propagare, (c) errore da loggare.
- [x] I siti (b) propagano l'errore; i siti (a) hanno un commento che dice
      perché è sicuro scartarlo.
- [x] Nessuna regressione nei test di workspace/storage/processor.

## Verifica

`make verify` → exit 0 (gofmt, standalone builds, deadcode, `go vet`,
`test-unit` su renderinggen/queue/objectstore). Race detector verde su
`internal/processor` (prefetch), `internal/overlay` (entity style), `internal/media`
(binaries) e `internal/repository/postgres` (job-type normalization).
