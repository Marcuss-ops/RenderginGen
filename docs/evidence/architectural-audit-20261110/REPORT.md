# Architectural audit follow-up — RenderingGen

Date: 2026-11-10. Scope: the static audit of the RenderingGen queue and the
`renderinggen/internal/overlay` hotspot. This report records what was changed,
what was measured, and what remains deliberately untouched. Every claim below
has the command that produced it in the verification log.

## 1. Queue — P1 fixes

### 1.1 In-memory queue state isolation

`queue/internal/repository/memory/repository.go` used to return the map's own
`*model.Job` from `claimCandidateLocked` → `ClaimState`, after the write lock
was released. Reads were shallow copies, so `render_plan`, `assets`,
`frame_range`, `not_before`, `progress` and the artifact graph (metrics map,
telemetry bytes, nested output facts, provenance) were still aliased.

Fix:

- Added deep-copy helpers (`cloneJob`, `cloneArtifact`, `cloneOutputFacts`,
  `cloneProvenance`, `cloneFloatMap`).
- Every insert stores a clone, so a caller mutating the slices it passed to
  `Submit` cannot rewrite stored state.
- `ClaimState`, `Get`, `Children`, `ByParent`, `ClaimFinalization` and
  `SubmitIdempotent` return caller-owned deep copies.
- `Complete`/`Rendered` store a deep copy of the artifact.

Regression tests: `memory/isolation_test.go` mutates every reference field of a
claim result, of the artifact passed to `Complete`, and of a `Get` result, then
re-reads and asserts the originals are untouched. A second test mutates a claim
result in a loop while another goroutine reads through the repository, so the
`-race` suite fails if the pointer ever escapes again.

### 1.2 Abandoned waiters in `WaitState`

`WaitState` registered a channel with the `Notifier` but never called `Forget`,
so a waiter that timed out or was cancelled stayed in the single-wake FIFO and
could consume a wake intended for a live caller.

Fix (`queue/internal/service/wait.go`): a deferred **closure**
(`defer func() { s.notify.Forget(wake) }()`) removes whichever channel is
currently parked. The closure matters: the channel is replaced after every
wake, so a deferred call with the channel as an argument would only ever
forget the first one.

Regression tests: `service/wait_forget_test.go` covers timeout, context
cancellation, the replacement channel parked after a terminal wake, and six
concurrent waiters, asserting the notifier's FIFO is empty in every case.

### 1.3 Idempotency parity between memory and PostgreSQL

Before this change the two backends disagreed:

| Scenario | Memory (before) | PostgreSQL (before) | Both (after) |
|---|---|---|---|
| Replay: same key, different id | canonical job, `created=false` | canonical job, `created=false` | canonical job, `created=false` |
| `Submit` reusing a stored key | **accepted**, idem index overwritten | rejected (`23505`) | rejected, `ErrJobExists` |
| `SubmitBatch` reusing a stored key | **accepted**, key overwritten | rejected, transaction rolled back | rejected, no partial family stored |
| `SubmitBatch` with the same key twice inside one batch | **accepted** | rejected (`23505`) | rejected by shared validation |
| `SubmitIdempotent` same id under a different key | `ErrJobExists` | **`sql.ErrNoRows`** (HTTP 500) | `ErrJobExists` |

Changes:

- `model.ValidateJobBatch` now rejects duplicate non-empty idempotency keys
  inside one batch — one shared rule, so neither backend can accept what the
  other rejects.
- The memory store validates keys against the store on **every** insert path
  (`Submit`, `SubmitIdempotent`, `SubmitBatch`) and indexes the key on all of
  them; `Submit` previously never touched the index at all. A conflicting batch
  is rejected before its first insert, matching the PostgreSQL rollback.
- PostgreSQL maps the unique-index violation (`23505`) on
  `idx_render_jobs_idempotency` or `render_jobs_idempotency_key_unique` to the
  repository's `ErrJobExists` sentinel, so the same conflict is a 409 on both
  backends instead of a driver-specific 500. Both index names are recognized
  because migration 007 created the first and migration 011 added the second,
  and PostgreSQL reports whichever it evaluated.
- `SubmitIdempotent` now propagates `ErrJobExists` when `Submit` failed on a
  duplicate id that owns no idempotency row, instead of returning the raw
  `sql.ErrNoRows`.

Regression tests: `postgres/idempotency_parity_test.go` runs the five scenarios
above against the real PostgreSQL repository and the in-memory repository in
the same test and requires identical outcomes; `memory/idempotency_test.go`
pins the store-side rules without a database.

### 1.4 PostgreSQL in CI

`.github/workflows/build.yaml` `test-queue` now:

- starts a `postgres:16` service container with a `pg_isready` health check,
- exports `TEST_DATABASE_URL` and `RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1`,
- runs the whole suite with `-race` (migrations, repository, concurrency and
  parity tests included),
- runs an explicit `-v` integration pass whose output is checked for
  `skipping postgres integration`; `set -o pipefail` preserves a test failure
  through `tee`, and a skip marker fails the job loudly.

`testDatabaseURL` (postgres tests) and the migrate integration test fail — not
skip — when `RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1` and the DSN is missing, so
an unwired service container can never turn the suite green by skipping.

## 2. Queue — P2 `ClaimState` quadratic fallback

The concurrent-tail pass in `ClaimState` compared each FIFO id against every id
in the snapshot with a nested loop, so an empty claim over a backlog of
deferred/anchor jobs was O(pending²). The snapshot now builds a `map[string]struct{}`
set once, and the tail pass is O(1) per id.

`memory/claim_bench_test.go` measures the exact path (all jobs deferred, so the
snapshot finds nothing and the tail pass scans the whole queue):

| Queue size | ns/op | B/op | allocs/op |
|---:|---:|---:|---:|
| 100 | 17,599 | 5,288 | 4 |
| 1,000 | 233,189 | 70,993 | 6 |
| 10,000 | 3,216,082 | 600,777 | 34 |

Growth is 13.3x for 10x jobs and 13.8x for the next 10x — near-linear, with the
residual factor explained by cache effects (the table is one run; expect normal
run-to-run variance). A return to the old algorithm would show ~100x per step.

## 3. Overlay package analysis (`renderinggen/internal/overlay`)

Measured with `go run tools/archaudit/main.go -dir renderinggen/internal/overlay`
(branch-count complexity heuristic, Go AST; cross-file references count uses of
top-level declarations owned by another file).

### Size

- 212 Go files: 119 test, 93 non-test.
- 44,675 lines, 1.74 MiB, 1,283 top-level functions.
- Largest files: `shape_comprehensive_suite_test.go` 1,709 lines;
  `compiler.go` 1,055 lines / 47.1 KiB; `overlay_v3_catalog_test.go` 802;
  `motion.go` 753; `editorial_v1_golden_plans_test.go` 750.

`compiler.go` is **not** a big-function module: its six functions total roughly
100 lines (`RenderPlanWireVersion`, `newPlan`, `CompileSemantic` and three JSON
marshalling methods). The other ~950 lines are the plan/layer type and constant
vocabulary.

### Complexity hotspots (non-test)

| Function | File | Lines | Complexity |
|---|---|---:|---:|
| `parseGradient` | `shape_colors_gradients.go` | 138 | 64 |
| `resolveAnimationPolicies` | `animation_policy.go` | 124 | 63 |
| `compileSemanticDecoded` | `semantic_compile.go` | 249 | 55 |
| `resolveSemanticItems` | `semantic_compile.go` | 133 | 54 |
| `validateSceneCamera` | `scene_camera_rig.go` | 90 | 52 |
| `validateMapTextStyle` | `semantic_map.go` | 45 | 52 |
| `compileImageItemCore` | `semantic_image_compile.go` | 133 | 49 |
| `compilePremiumComponent` | `premium_image_recipe.go` | 161 | 48 |
| `validateRuntimeImageStyleKey` | `runtime_style.go` | 59 | 48 |
| `parseStyleBlock` | `visual_style_resolver.go` | 153 | 44 |
| `compileChainItem` | `chain_compile.go` | 129 | 44 |

The largest test files also carry very high branch counts
(`TestRuntimeAnimationUseCasesSeparateCountedCompositions` 90,
`TestEveryCallableCatalogMotionExecutesOnTheStrictGPU` 88,
`TestGoldenEditorialV1MegaCanary` 74), which is expected for scenario tests but
makes them expensive to change.

### Coupling

| File | Incoming identifier uses | Referencing files |
|---|---:|---:|
| `compiler.go` | 2,442 | 175 |
| `semantic_item_contract.go` | 1,150 | 171 |
| `registry.go` | 314 | 38 |
| `official_presets.go` | 239 | 45 |
| `asset_registry.go` | 206 | 63 |
| `plan_wire.go` | 153 | 74 |

Widest fan-out: `semantic_compile.go` 195, `semantic_image_compile.go` 159,
`premium_image_recipe.go` 145, `motion.go` 103.

### Change amplification

Over the 157 commits that touch the package: mean 7.6 files per commit, median
4, p90 22, max 64; 42% of commits touch more than 5 files. The 71 commits that
touch `compiler.go` touch a mean of 11.5 overlay files (median 7, max 64).

### Recommendation

Do not start by splitting `compiler.go`. Its size is type vocabulary, and its
coupling is package-wide (175 referencing files); moving it would relocate a
shared contract without reducing the number of files a rule change touches.
Priority order, by measured complexity and amplification:

1. `semantic_compile.go`: break `compileSemanticDecoded` (249 lines,
   complexity 55) into per-item-kind units behind the existing contract, and
   `resolveSemanticItems` (133/54) into validation vs lowering passes. This file
   also has the widest fan-out (195).
2. `animation_policy.go` / `shape_colors_gradients.go`: `resolveAnimationPolicies`
   (124/63) and `parseGradient` (138/64) are pure, high-branch functions that
   can be split and unit-tested without touching the compiler contract.
3. Test harnesses: extract shared fixtures from the >600-line test files
   (`shape_comprehensive_suite_test.go` 1,709 lines, `overlay_v3_catalog_test.go`
   802, `catalog_motion_gpu_test.go` 673) — several of them re-declare harness
   helpers, and they dominate commit amplification.
4. Only then consider splitting the `compiler.go` vocabulary (plan wire types
   vs layer effects), keeping type aliases so the 175 referencing files do not
   change; the split is justified only if it also reduces the shared type
   surface.

The numbers are a heuristic (identifier-based references, branch-count
complexity), not a certified gocyclo/staticcheck report; use them to rank
candidates, then confirm with the compiler and the existing golden tests.

## 4. Historical artifact inventory

Measured over tracked files in the RenderingGen repository
(`git ls-files` + working-tree sizes):

- 1,873 tracked files, 521.1 MiB total.
- 181 files ≥ 1 MiB, together 424.1 MiB (81.4% of tracked bytes).
- By directory: `UploadDrive/` 420.9 MiB (80.8%), `assets/` 57.8 MiB,
  `hdr_bokeh_focus_pull_20261108/` 15.0 MiB, `Upload/` 9.8 MiB,
  `renderinggen/` (all Go) 10.5 MiB.
- By extension: `.png` 263.9 MiB, `.mp4` 196.8 MiB, `.mp3` 41.9 MiB,
  `.log` 4.9 MiB.
- Largest single files: `SECRET_DIARIES_voiceover_en_REMY.mp3` 39.6 MiB,
  `assets/backgrounds/background-wwe.mp4` 20.0 MiB,
  `background-music.mp4` 12.3 MiB, `background-crime.mp4` 9.4 MiB,
  `background-boxe.mp4` 9.3 MiB, `background-discovery.mp4` 6.6 MiB.

Evaluation:

- **Keep in Git**: source (`renderinggen/`, `queue/`, `objectstore/`), contracts
  and schemas, migrations, small golden fixtures, docs and evidence notes under
  a few hundred KiB.
- **Move out**: `UploadDrive/` (420.9 MiB of publication outputs),
  `assets/backgrounds/*.mp4` (57.8 MiB of reusable input media),
  root-level render outputs (`hdr_bokeh_focus_pull_20261108/`, `Upload/`,
  `test_*.png`/`.mp4`), and `.log` build logs. Input media belongs in the
  object store with a manifest of content hashes; published outputs belong in
  the artifact store, not in the source tree.
- Removing these files from HEAD reclaims nothing for existing clones: the
  bytes are already in history. Reclaiming them requires a history rewrite
  (`git filter-repo`/BFG), which is destructive and must be scheduled with a
  full backup and a freeze; it is **not** part of this task. The cheap,
  reversible first step is `.gitignore` patterns plus a CI size gate (for
  example, fail a PR that adds a tracked file > 1 MiB outside an allow-list).
- No file was deleted, moved or rewritten by this audit.

## 5. Verification log

All commands run from `RenderingGen/queue` unless noted. PostgreSQL 16 in
Docker (`postgres://renderinggen:renderinggen@127.0.0.1:55432/renderinggen`),
the same image and health check as CI.

| Check | Result |
|---|---|
| `test -z "$(gofmt -l .)"` | clean |
| `go build ./...` | ok |
| `go vet ./...` | ok |
| `TEST_DATABASE_URL=… RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 go test -race -count=1 ./...` | all packages ok (postgres 9.4s) |
| `go test -run '^$' -bench BenchmarkClaimStateDeferredBacklog -benchmem ./internal/repository/memory/` | table in §2 |
| Workflow YAML parse assertions (`postgres:16`, DSN, require flag, integration step) | ok |
| `go run tools/archaudit/main.go -dir renderinggen/internal/overlay` | tables in §3 |

New regression tests: `memory/isolation_test.go`, `memory/idempotency_test.go`,
`memory/claim_bench_test.go`, `service/wait_forget_test.go`,
`postgres/idempotency_parity_test.go`; `testDatabaseURL` and the migrate
integration test fail closed under `RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1`.

## 6. Second audit — staging, cache, staging IDs, finalizer and batch recovery

Items 6–13 were present in the working tree when this pass started. This pass
checked every item against the code, deleted the remaining dead fragment in
`isCacheCorrupt`, and added the two acceptance cases that were missing: the
real migration-007 index name and a hundreds-of-leases recovery.

### Removed paths

The faulty implementations are gone rather than kept behind a flag or fallback:

- `StagingWrite` no longer publishes `{session}.pending` and `{session}.sha256`
  outside a lock; the old pairing is replaced by per-session serialization and
  a finalize-time hash of the bytes being installed.
- `ClaimFinalization` no longer accepts `pending`/`running` on state alone.
- `RequeueExpired` no longer selects the whole expired set in one transaction.
- `SubmitIdempotent` no longer re-reads the idempotency key after every insert
  failure.
- `memCache.Get` no longer returns the cache's own slice.
- `diskCache.Get`/`Path` no longer return a hit on existence alone.
- `stagingSessionID` no longer truncates after appending the random suffix.
- The unused `syscallErr` declaration in `isCacheCorrupt` was deleted.

### Item-by-item

| # | Failure mode | Replacement | Regression test |
|---|---|---|---|
| 6 | Concurrent writers could pair one attempt's bytes with another attempt's digest; finalize trusted the sidecar. | `stagingSessionLocks` (64 shards) serializes write-commit, finalize and abort per session; the digest sidecar is written temp → fsync → rename; finalize re-hashes the pending file immediately before the install rename. | `objectstore/internal/store/staging_test.go`: barrier-synchronized writers, finalize, then SHA-256 of the bytes actually installed; tampered pending bytes are rejected. |
| 7 | `install` could add an over-budget object, evict it immediately, and still return its path. | `admissible` rejects an object larger than the whole L2 budget before it is indexed/committed; the oversized object is streamed to an uncached temporary path instead of a cache path. | `renderinggen/internal/storage/l2_integrity_test.go`: cache smaller than the object, no stale path returned. |
| 8 | A cache hit returned bytes without proving `sha256(bytes) == key`; corruption could be promoted to L1. | `Get` and `Path` call `contentVerified`; a size+mtime stamp remembers the verified file identity, concurrent cold readers share one hash pass, and a mismatch drops/removes the corrupt entry so L3 refetches it. | `l2_integrity_test.go`: bytes modified under an unchanged SHA-256 key are treated as a miss and are not promoted. |
| 9 | `memCache.Get` exposed the internal `[]byte`; a caller write mutated shared cache state. | `Get` returns a copy; `Put` also copies, so the cache never shares a writable buffer with a caller. | `renderinggen/internal/storage/l1_ownership_test.go`: mutating a returned slice does not change the cached value or a later `Get`. |
| 10 | `stagingSessionID` appended 16 hex characters then truncated to 128, erasing the random tail for 128–256-byte job IDs and making retries share a session. | The 17-byte suffix is reserved first; only the job-derived prefix is truncated. | `renderinggen/internal/processor/tail_upload_session_test.go`: 111, 112, 128 and 256-byte IDs keep the suffix and produce distinct sessions. |
| 11 | The finalizer could claim an ordinary running job, overwrite `current_worker_id` and `lease_until`, and take it from an active worker. | PostgreSQL adds `EXISTS (child.parent_job_id = parent.id)` to the claim; the memory backend applies the same anchor check. A non-anchor is rejected without changing state, owner or lease. | `queue/internal/repository/postgres/finalize_sql_test.go`; `queue/internal/repository/memory/finalization_test.go`. |
| 12 | Lease recovery selected every expired job, locked all rows, and did per-job statements in one 15-second transaction. | `RequeueExpired` drains bounded batches (`LIMIT`, `FOR UPDATE SKIP LOCKED`, configurable via `SetRequeueBatch`), each in its own transaction and event `INSERT`; the loop ends on a short batch. | `queue/internal/repository/postgres/requeue_batch_test.go`; `requeue_scale_test.go`: 250 expired leases, batch 25, all recovered, pending=250/running=0. |
| 13 | `SubmitIdempotent` re-read the key after any insert error, so a validation error could be replaced by `sql.ErrNoRows` (or another submitter's row). | `isSubmitConflict` accepts only the `ErrJobExists` sentinel or a `23505` whose constraint is an idempotency index (`idx_render_jobs_idempotency` from migration 007 or `render_jobs_idempotency_key_unique` from migration 011); every other insert error is returned unchanged. | `submit_idempotent_test.go`, `idempotency_parity_test.go`, `submit_conflict_index_test.go` (both index names). |

### Verification (second audit)

| Check | Result |
|---|---|
| `gofmt -l` on the changed Go files | clean |
| `cd objectstore && go test -race -count=1 ./...` | all packages ok |
| `cd renderinggen && go test -race -count=1 ./internal/storage/...` | ok |
| `cd renderinggen && go test -race -count=1 -run 'TestTailUploadStagingSessionID|TestStagingSession' ./internal/processor/...` | ok |
| `cd queue && TEST_DATABASE_URL=… RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 go test -race -count=1 ./...` | all packages ok (postgres 17.3s) |
| `go test -v -run 'TestRequeueExpiredDrainsHundredsAcrossBatches|TestIsSubmitConflictRecognizesBothIdempotencyIndexes|TestClaimFinalizationRejectsNonAnchorRunningJob'` | pass (250-job recovery 3.35s) |

PostgreSQL was the same `postgres:16` image CI uses, exposed on
`127.0.0.1:55432`, and the suite ran with
`RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1`, so a missing database fails instead of
skipping.

## 7. Structural P0 — lease fencing for job ownership

Fencing is now the contract phase. Migration `030_lease_generation.sql` adds
`render_jobs.lease_generation BIGINT NOT NULL DEFAULT 0`. Every successful
`Claim` and `ClaimFinalization` increments it atomically and returns it on the
job as `lease_generation`. The six worker transitions — `Complete`, `Rendered`,
`Fail`, `FailPermanently`, `Renew`, `SetProgress` — require the generation and
include it in their conditional UPDATE / row-lock predicate. A report from an
attempt whose lease expired and was re-claimed matches zero rows and is returned
as a lost lease (HTTP 409), even when the worker id is the same. Generation 0 is
rejected at the API boundary: there is no legacy fallback.

### What changed

- **Wire/model** — `client.Job.LeaseGeneration` (`json:"lease_generation"`)
  and `model.Ownership{JobID, WorkerID, Generation}`.
- **PostgreSQL** — `ClaimState` selects and increments the generation, sets it
  in the claim UPDATE and returns it; `ClaimFinalization` increments it; `Get`
  projects it. All six transitions include the generation in the ownership
  predicate. `FencedRepository` and the `*Owned` split were deleted.
- **Memory** — `claimCandidateLocked` and `ClaimFinalization` increment the
  generation; `runningJob` requires an exact match for every transition.
- **Service/HTTP** — every public transition takes `generation int64`; a
  missing or non-positive token is rejected with `400 Bad Request`. The
  `*Owned` methods were removed, and `/renew` and `/progress` now require
  `lease_generation` like the terminal routes.
- **Queue client/worker** — one API (`Complete`, `Rendered`, `Fail`,
  `FailPermanently`, `Renew`, `ReportProgress`) takes the generation; the
  report body always serializes it and rejects `<= 0`. The `*WithLease`
  variants and the generation-0 fallback were removed.
- **RenderingGen** — the wrapper and processor pass `job.LeaseGeneration` to
  terminal, renewal and progress calls. The progress tracker snapshot carries
  `lease_generation`, so the throttled pusher is fenced too.
- **Compatibility** — intentionally broken: clients that omit the token are
  rejected. Queue and workers must be upgraded together; no legacy path remains.

### Tests

| Test | Proves |
|---|---|
| `memory/lease_generation_test.go::TestLeaseGenerationFencesStaleTransitions` | Same worker re-claims after expiry; stale `Fail`, `Renew` and `SetProgress` are rejected, while the current generation succeeds. |
| `postgres/lease_generation_test.go::TestLeaseGenerationFencesStaleTransitions` | PostgreSQL parity for all three transitions, including the generation advance. |
| `server/lease_generation_test.go::TestLeaseGenerationRejectsStaleHTTPReport` | Claim carries the token; a stale HTTP report returns 409; the current attempt's report returns 204. |
| `server/lease_generation_test.go::TestLeaseGenerationRequiredForRenewAndProgress` | `/renew` and `/progress` reject a missing token with 400 and accept the claimed generation. |
| `client/lease_generation_test.go::TestCompleteSendsGeneration`, `TestReportProgressSendsGeneration` | The token is serialized on terminal and progress reports. |
| `processor/lease_generation_test.go::TestTerminalReportsCarryLeaseGeneration` | All terminal report paths carry the claimed generation. |
| `progresspush/progresspush_test.go::TestPushOnceReportsTrackedJob` | The progress pusher forwards the tracker snapshot's `lease_generation`. |

### Verification

| Check | Result |
|---|---|
| `cd queue && TEST_DATABASE_URL=… RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 go test -race -count=1 ./...` | all packages ok (postgres 14.1s) |
| `cd renderinggen && go test -race -count=1 ./internal/processor/... ./internal/queue/... ./cmd/renderinggen/... ./internal/progresspush/... ./internal/chronon/...` | all ok (processor 32.8s) |
| `gofmt -w` on every changed Go file | clean |

### Remaining in the fencing program

- `Renew` and `SetProgress` now use the same generation predicate as the
  terminal transitions; the stale-attempt holes are closed in both backends.
- The contract is intentionally not mixed-version compatible. Upgrade the
  queue and every worker together; an old client now fails with 400/409 instead
  of silently taking a generation-0 path.
- `refactored/third_party/queue` was re-synced from this working tree so the
  PipelineGen vendored build no longer carries the pre-fencing implementation.

## 8. Residual risk and follow-ups

- `refactored/third_party/queue` was re-synced from the `RenderingGen/queue`
  working tree on 2026-11-10 (excluding `VENDOR.md`), and the host fixtures were
  refreshed per the vendoring procedure. It now contains migration 030 and the
  contract-phase fencing code; `VENDOR.md` records the new provenance.
- The in-memory backend is now behaviorally aligned with PostgreSQL, but it is
  still non-durable by design; parity tests are behavioral, not persistence
  tests.
- Complexity and coupling numbers are heuristics (see §3) and should be
  re-measured before any refactoring decision; the tool is committed at
  `tools/archaudit/main.go` so the measurement is reproducible.
- CI execution of the new PostgreSQL job was validated locally against the same
  image and by YAML assertions; it has not run on GitHub Actions from this
  checkout.
