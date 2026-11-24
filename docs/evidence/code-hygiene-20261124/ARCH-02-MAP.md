# ARCH-02 — processor responsibility map e prescale

Data: 2026-11-24. Baseline: 46 file Go non-test, 20.135 righe; il file più
grande era `prescale_images.go` (443 righe).

## Responsabilità

| Gruppo | File | Dipendenze interne |
|---|---|---|
| Pipeline e prepare | `processor.go`, `pipeline.go`, `prepare*.go` | overlay, queue, workspace, storage |
| Materialize e asset | `materialize.go`, `asset_merge.go`, `prepare_assets.go` | workspace, storage, overlay |
| Prescale | `prescale_images.go` | overlay, storage, queue |
| GPU e sessione | `gpu_*.go`, `canonical_*.go`, `receipt_*.go` | chronon, media, storage |
| Finalize e probe | `finalize_job.go`, `finalization.go`, `segments.go` | queue, chronon, media |
| Storage e publish | `store_artifact.go`, `publish_drive.go`, `tail_upload.go` | storage, drive, queue |
| Report e policy | `report.go`, `publication.go`, `gpu_execution_policy.go` | queue, overlay |

Le dipendenze sono un DAG: root `processor` → `overlay`/`queue`/`storage`/
`chronon`/`workspace`; nessuno di questi importa `processor`.

## Estratto: `internal/processor/prescale`

Sette helper puri sono passati nel sottopackage:

- `Extension`, `CachePath`, `LoadCache`, `WriteAtomicFile`
- `DisplayBoundSize`, `ResizeAreaAverage`, `Encode`

Il package importa solo standard library, `image` e `storage`; **non** importa
`processor`, `overlay` o `queue`. Il root conserva wrapper con gli stessi nomi,
quindi i call-site e i test esistenti non cambiano. `prescale_images.go` scende
da 443 a 318 righe e la policy di ridimensionamento è testabile senza un
`Processor`.

Il guard `TestPrescaleSubpackageIsIndependent` impedisce una dipendenza di
ritorno.

## Verifica

- `go test ./internal/processor/prescale ./internal/processor` → verde.
- `go build ./...` nel modulo renderinggen → verde (gate finale).
- I test di `applyPrescaledImages` e del prepared package restano verdi, quindi
  il comportamento di piano non cambia.
