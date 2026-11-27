# Artifact lifecycle, orphans and garbage collection

This document is the operational contract for objects in the object store. It
describes the states an artifact passes through, who writes each one, what may
consume it, and how unreferenced bytes are collected.

## States

| State | Bytes | Ledger row | Who writes it | Consumable? |
|---|---|---|---|---|
| `staged` | uploaded by the streaming path while the render finishes | no | `renderinggen` (`stagedUpload`) | **no** |
| `verified` | present; hash, segments and composition policy accepted | no | `renderinggen/internal/processor` (`storeArtifact`) | **no** |
| `ledgered` | present | yes (`render_artifacts`) | Queue `Complete` called by the worker finalizer | **yes** |
| `orphan` | present | **no** | any crash/failure between `verified` and `ledgered` | **no** |

The only condition that makes an artifact consumable is the ledger row. The
content address says *what the bytes are*, never *that they were certified for
a job*: two jobs with identical bytes share an address, but only the job with a
ledger row owns the result.

### Order inside `storeArtifact`

1. `resolveStoreIdentity` — staged digest, receipt identity, or a direct hash;
2. `attachBatchSegments` — may reject;
3. `ingestChrononTelemetry` — may reject;
4. `preserveRawTimingSidecar` — best effort;
5. `publishArtifactBytes` — installs the bytes (skipped when staging already did);
6. `recordArtifact` — best-effort worker-local mirror;
7. the caller writes the queue ledger row (`queue.Complete`).

The certification gates (steps 2–4) run before publication, so a job rejected
by certification never reaches the store unless it used streaming staging. A
staged upload is the only case where bytes arrive before step 5; it is safe
because step 7 is still the only consumption gate. The local mirror is written
after publication and is diagnostic only; a mirror write failure is
non-fatal.

### Failures and what they leave

| Failure | Bytes | Ledger | Job | Retry |
|---|---|---|---|---|
| composition/segment/telemetry rejected | none (staged upload is aborted by `FinalizeJob`) | none | failed | new attempt, new bytes |
| publication failed | none or a partial staging file | none | failed | hash-then-PUT fallback or new attempt |
| worker-local mirror write failed | present | queue completion proceeds | can complete | metric records the mirror failure |
| `queue.Complete` failed after publication | present | none | not `COMPLETED` | object is an orphan until the retry reuses the address |

A retry that publishes the same bytes reuses the same address; the row is then
written against the same object. An orphan is therefore not "a broken object",
only an object with no owner *yet*.

## Garbage collection

The object store is a CAS and cannot know which addresses the ledger still
owns. Collection is reference-driven:

1. **Export the referenced addresses from the queue ledger (read-only).**
   Both the clip and the raw-timing sidecar are references; exporting only the
   clip would collect a sidecar the row still points at.

   ```sh
   DATABASE_URL='postgres://…/renderinggen?sslmode=disable' \
     go run ./cmd/artifact-keys -out /var/tmp/referenced-keys.txt
   ```

2. **Dry run the store.** The default is a dry run; the report names every
   unreferenced object and the reason.

   ```sh
   objectstore -gc \
     -data-dir /var/lib/objectstore \
     -referenced /var/tmp/referenced-keys.txt \
     -min-age 24h
   ```

   Log lines: `gc scan: … orphans=N orphan-bytes=B …`,
   `GC ORPHAN key=… path=… bytes=… modified=… reason=…`,
   `GC STAGING path=…`.

3. **Delete only after reviewing the report.**

   ```sh
   objectstore -gc -delete -min-age 24h \
     -data-dir /var/lib/objectstore \
     -referenced /var/tmp/referenced-keys.txt
   ```

`-min-age` protects an object whose publication is still in flight: the export
is a snapshot, and a worker may have installed the bytes after it. 24h is the
shipped default; raise it if render retries can take longer.

### Safety rules

- An address in the reference file is **never** deleted, whatever its age.
- The store refuses to run with an empty reference set: an empty export is a
  misconfiguration, not a policy to delete everything.
- A non-canonical ledger key is reported and skipped by the export; it cannot
  address an object.
- A non-canonical filename or a misplaced shard directory is collected like an
  orphan: no reader can ever resolve it.
- Interrupted uploads (`.object-*`) follow the same age rule and are reported
  separately.
- `-gc` never serves traffic; run it from a maintenance job, not on the live
  process.

### Suggested cadence

Run the export + dry run daily and keep the log; delete weekly after review.
The orphan count and byte count in the `gc scan:` line are the observable
metric — a jump means publications are failing between "verified" and
"ledgered", which is worth investigating before it becomes a disk alert.

## Manual recovery

- **Missing sidecar referenced by a job**: re-render the job; the sidecar is
  best effort and its absence is not corruption.
- **Object present but job not `COMPLETED`**: do not "repair" the ledger by
  hand. Let the lease expire or fail the job; a re-render reuses the address.
- **Integrity violation** (`objectstore -verify-all`): the bytes contradict
  their address. Re-publish from the producer; never edit bytes in place.
