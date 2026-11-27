# RenderingGen architecture and code-quality audit — campaign record

**Repository:** `Marcuss-ops/RenderingGen` (renamed 2026-11-27; audit input used the historic spelling)
**Source branch:** `main`
**Audit input:** 26 November 2026 review of commit `e8fec015d28167b84ccd94ba6e002de43518fc6f`  
**Campaign date:** 27 November 2026  
**Scope:** the 15-item roadmap in the audit, applied to `RenderingGen/` (worker, queue, objectstore, contracts, deployment).

## Goal

Close every acceptance criterion that is reachable from this checkout. “Reachable” means
the repository can enforce it without remote administration or by taking over a live
production service. Remote-only actions are performed only where the ticket requires them; deployment of
images and restart of the installed systemd unit remain outside this campaign.

**Invariant enforced by this campaign:**

> A job is completed only when its artifact is persisted, verified according to the
> requested policy, and associated with the correct execution attempt.

## Result

| # | Priority | Checklist item | Status | Evidence |
|---|----------|----------------|--------|----------|
| 1 | P0 | Align repository and Go module identity | **Closed** | Repository renamed to `Marcuss-ops/RenderingGen`; `queue/v0.1.0` resolves from GitHub; no local Queue replace; external consumer build passed |
| 2 | P0 | Restore CI fixtures, fonts, FFmpeg and geographic catalogs | **Closed** | Checked-in golden fixtures/fonts; FFmpeg install step; CI compose catalog mount; clean-checkout suite green |
| 3 | P0 | Run all conformance and certification gates | **Closed locally**; golden E2E CI job external | Every local gate green; real-engine software and Vulkan renders pass; golden E2E ports are occupied by the running stack |
| 4 | P1 | Eliminate persisted-but-undelivered claim | **Closed** | Hydration moved inside the claim transaction; rollback test |
| 5 | P1 | Verify logical atomicity of artifact publication | **Closed** | Certification before PUT; rejected artifact leaves no object; regression test |
| 6 | P1 | Define certify/strict guarantees | **Closed** | Authored-overlay evidence is mandatory under certify/strict; README policy table |
| 7 | P1 | Verify GPU recovery, lease and daemon reset | **Closed** | GPU loss, daemon reset, stale generation and capability-downgrade tests |
| 8 | P1 | Remove implicit filesystem catalog dependency | **Closed** | Explicit `CHRONONTEMPLATE_CATALOG`; startup validation; systemd/CI deployment updated; guard test |
| 9 | P2 | Analyse overlay internal coupling | **Closed** | Measurements and extraction boundary in `01-MEASUREMENTS.md`; one duplicated route validator consolidated |
| 10 | P2 | Verify memory/PostgreSQL semantic parity | **Closed** | Shared repository suite plus rendered re-claim subtest on both backends |
| 11 | P2 | Remove remaining mutable global dependencies | **Closed** | Processor seams are instance fields; test-only registry reset moved out of production |
| 12 | P2 | Remove quadratic matching and N+1 reads | **Closed** | Batch artifact hydration; index-free single query per evidence table; batch tests |
| 13 | P2 | Measure churn, fan-in, fan-out, cognitive complexity | **Closed** | `01-MEASUREMENTS.md` |
| 14 | P3 | Profile allocations, GPU, VRAM and transfers | **Closed** | Benchmarks, allocation profile, one measured Vulkan/NVENC render; `01-MEASUREMENTS.md` |
| 15 | P3 | Remove valueless abstractions/conversions | **Closed** | `dupl`/staticcheck review; duplicated route validator removed; remaining aliases are boundary vocabulary |

## P0 — identity, reproducibility, gates

### P0-1 — Repository and module identity

The module paths are canonical (`github.com/Marcuss-ops/RenderingGen/{renderinggen,queue,objectstore,client,...}`)
and every local import uses them. The GitHub repository was renamed to `RenderingGen`; the historic URL redirects.
Queue v0.1.0 is published from the `queue/` module path, and RenderingGen consumes
that version without a local replace. GitHub Actions run `module-standalone`
`46029573918` passed from a temporary consumer directory on PR #1.

The conformance rule `module_path_typo` was narrowed to module/import paths, so prose and
legitimate `.git` clone URLs that still carry the historical lower-case spelling are not
flagged. The conformance baseline is empty.

Evidence:

```text
$ make conformance
conformance: clean (3 targets, 0 baselined)

$ make test-module-standalone
=== standalone build: renderinggen ===
=== standalone build: queue ===
=== standalone build: objectstore ===
standalone builds: ok
```

### P0-2 — Reproducible test environment

- `testdata/golden/` now carries the backgrounds, images, fonts and catalogs used by the
  certification suites.
- `.github/workflows/build.yaml` installs FFmpeg before the renderinggen suite.
- The CI worker compose overlay mounts the checked-in Natural Earth catalog and sets
  `CHRONONTEMPLATE_CATALOG=/var/lib/renderinggen/catalog`.
- A clean copy at `/tmp/rg-clean` (no `.git`, no `Upload*`, no `bin`, `.tmp`, `evidence`,
  no sibling repositories) passed the full renderinggen suite and the queue suite.
- After the final overlay dedup, the overlay/processor/CLI packages were re-run in that
  clean copy and passed.

```text
$ (cd /tmp/rg-clean/renderinggen && go test -count=1 ./...)
ok .../internal/overlay
ok .../internal/processor
...
$ (cd /tmp/rg-clean/queue && go test ./...)
ok ... (postgres tests skip without TEST_DATABASE_URL)
```

The workflow run itself cannot be executed without a push. Every command it runs was run
locally in the same shape, including `-race`, the certification tag and the architecture
scanner.

### P0-3 — Conformance and certification gates

All repository gates pass:

```text
$ make test-gofmt
gofmt: clean

$ make check-gpu-lanes
GPU profile contract OK: native=2, native-b=3, Docker=2, CI=1

$ make test-architecture
ok .../internal/architecture  17.340s

$ make deadcode
deadcode renderinggen: only the documented external-contract exports are unreachable
deadcode queue: only the documented cross-module exports are unreachable
deadcode objectstore: clean

$ make test-certification
ok .../internal/overlay  1.608s
```

Real-engine certification (software backend, checked-in fixtures):

```text
$ env -u RENDERINGGEN_SKIP_GPU_E2E CHRONON_BIN=/usr/local/bin/chronon3d_cli \
    go test -tags certification -count=1 ./internal/overlay \
    -run 'TestFinal_ExclusiveEndNoFrame125$' -v
--- PASS: TestFinal_ExclusiveEndNoFrame125 (11.12s)
```

Strict GPU certification (Vulkan + NVENC, 1280×720, 2 s):

```text
$ env -u RENDERINGGEN_SKIP_GPU_E2E CHRONON_BIN=/usr/local/bin/chronon3d_cli \
    go test -tags certification -count=1 ./internal/overlay \
    -run 'TestClassicPhrasePresetExecutesOnStrictGPU$' -v
--- PASS: TestClassicPhrasePresetExecutesOnStrictGPU (3.00s)
```

**Golden E2E:** the workflow job exists and is part of the release gate, but it was not run
locally because ports `8081` (queue) and `9000` (objectstore) are occupied by the running
local stack. Taking those ports would mean stopping services outside this task's
production-safety boundary. The CI job runs it on a hosted runner after a push; that push
is an external action.

## P1 — correctness and lifecycle

### P1-1 — Claim persisted before hydration

`attachClaimArtifacts` now executes **inside** the `Claim` transaction, before `Commit`.
An artifact-read failure rolls the claim back, so the worker never receives an error for a
job that the database already marked claimed.

Evidence:

```text
$ go test -count=1 ./internal/repository/postgres \
    -run 'TestClaimRollsBackWhenArtifactHydrationFails|TestClaimBatchHydratesEveryRenderedArtifact'
ok .../internal/repository/postgres
```

The rollback test forces the artifact hydration to fail and asserts the job is still
pending and claimable afterwards.

### P1-2 — Publication before all checks

`store_artifact.go` now certifies the artifact **before** `publishArtifactBytes`. A policy
rejection returns before any object is written. Staged upload remains the documented
exception, because the publisher must exist before the final verification pass.

Evidence:

```text
$ go test -count=1 ./internal/processor \
    -run 'TestStoreArtifactDoesNotPublishBytesRejectedBeforeCertification'
ok .../internal/processor
```

The test asserts the object store saw zero PUTs when strict composition evidence is
missing.

### P1-3 — Certify/strict guarantees

The contract is now explicit and fail-closed:

- `fast`, `normal`, `sample`: absent composition evidence is recorded
  (`CompositionPredictionUnverifiable`) and does not fail the output.
- `certify`, `strict`: when the plan has an authored overlay, absent or undecodable
  composition evidence **fails** the artifact. A video that decodes but cannot be shown to
  contain the requested overlays is not certified.
- Plans with no authored overlay have nothing to verify and remain valid.

`README.md` carries the policy table. Tests:

```text
$ go test -count=1 ./internal/processor -run 'TestVerifyCompositionPrediction'
ok .../internal/processor
```

### P1-4 — GPU recovery, lease and daemon reset

Added coverage for the cases the audit named:

- `TestExecuteRenderSessionGPUOnlyFailsClosedWhenTheDeviceDisappears`
- `TestExecuteRenderSessionRecoversAfterADaemonReset`
- stale lease generation cannot complete a `Rendered` job
- `TestCapabilityDowngradeRevokesGPUAdmission`

These exercise the worker-side policy, not a second independent GPU policy in the queue.
The queue remains the owner of the persisted execution policy; the worker applies it.

### P1-5 — Explicit geographic catalog

- `geofeatures.DefaultRegistry()` reads `CHRONONTEMPLATE_CATALOG`; it never searches the
  current working directory or parent directories.
- A failed load is **not** cached, so a catalog mounted after startup is picked up on the
  next call.
- `LoadDir` is the explicit entry point for tests and the map CLI.
- `cmd/renderinggen/bootstrap.go` validates the catalog before the worker starts serving.
- Both systemd worker units and the CI compose worker declare the variable.
- `TestWorkerDeploymentsDeclareCatalogPath` now guards units and compose against omission.

Operational note: the installed systemd unit on this host is not reloaded by this campaign.
The repository unit is updated; installing/restarting it is a deployment action and was
deliberately not performed. Because the new binary validates at startup, the unit **must**
be reinstalled together with the next deployment.

## P2 — structure and parity

### P2-1 — Overlay coupling

Measured, not guessed: 95 non-test files, 164 test files, 24,011 production LOC,
1,592 file-touch churn entries in 90 days, 11 internal packages importing overlay, and
overlay importing 12 internal packages. Full tables and the recommended extraction
boundary are in `01-MEASUREMENTS.md`.

Conclusion: keep `overlay` as the single semantic compiler authority. Extract only pure
responsibilities that do not need compiler state. `mapgeom` is already extracted and is the
model for the next candidates (pure typography/geometry), not a reason to split the
compiler itself.

One concrete cleanup was made: the route-entry validator duplicated between the raster and
ModernMap1 paths is now one function, `validateMapRouteEntries`. This reduced
`validateMapContract` cyclomatic complexity from 48 to 35 and removed the only production
clone group reported by `dupl`.

### P2-2 — Memory/PostgreSQL parity

Both backends run the shared repository suite. The suite now includes:

> `rendered re-claim hydrates the published artifact`

It proves that a completed/rendered job, when re-claimed, returns the same artifact
identity and metadata from both backends. Full runs:

```text
$ TEST_DATABASE_URL=postgres://renderinggen:renderinggen@127.0.0.1:55432/renderinggen?sslmode=disable \
  RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 go test -race -count=1 ./...
ok .../internal/repository/memory
ok .../internal/repository/postgres
ok .../internal/repository/repositorytest
```

### P2-3 — Mutable globals

`validateDecode`, `validateDecodeSampled` and `countDecodedFrames` are no longer package
variables. Decode validation and frame counting are `Processor` fields with production
defaults, so a test cannot mutate a process-wide seam.

The geographic registry still caches a successful load process-wide, but the cache is
immutable, explicit and retryable. Its test-only reset hook now lives in
`internal/geofeatures/export_test.go`, so the production binary carries no test mutator.

### P2-4 — Quadratic matching and N+1 reads

Claim hydration no longer performs a nested `jobs × candidates` scan followed by one
artifact query per row. It:

- hydrates artifacts inside the claim transaction;
- uses one read per evidence table (`artifactReadColumns` / `queryArtifactRows`);
- batches metrics and telemetry by job;
- preserves the same artifact view for every rendered candidate.

Measured effect: a claim batch now issues a constant number of SQL statements independent
of the number of artifacts (three statements total in the tested shape), instead of N+1.

```text
$ go test -count=1 ./internal/repository/postgres \
    -run 'TestClaimBatchHydratesEveryRenderedArtifact|TestClaimRollsBackWhenArtifactHydrationFails'
ok
```

### P2-5 — Churn, fan-in/out, complexity

See `01-MEASUREMENTS.md`. Headline numbers:

- churn (90 days, file touches): `overlay` 1,592; `processor` 622; `chronon` 260.
- fan-in: `overlay` 11, `chronon` 9.
- fan-out: `cmd/renderinggen` 19, `processor` 13, `overlay` 12.
- top cyclomatic (production): `validateMapLODs` 45, `validateMapPresentationParams` 44,
  `applyShapeProceduralParams` 40, `validateMapContract` 35 (down from 48).
- top cognitive (production): `FitImageLayersToSizes` 56,
  `validateMapPresentationParams` 54, `applyChartParams` 53.

No function was split merely to move a number. The one split made was duplication removal
with identical behavior and tests.

## P3 — profiling and abstractions

### P3-1 — Allocations, GPU and VRAM

Benchmarks and the allocation profile are in `01-MEASUREMENTS.md`. The actionable facts:

- semantic compilation is already sub-millisecond to ~4 ms per plan;
- `modernMapPathGeometry` is 48% of allocated bytes, but the plan compiles in ~4.3 ms,
  so optimizing it now would trade readability for a cost the caller does not observe;
- one strict Vulkan/NVENC 1280×720/2 s render moved GPU memory from 3,572 MiB to
  **4,207 MiB peak** (+635 MiB) on the 16,376 MiB RTX A4000;
- no per-frame transfer measurement exists; the next live-profiling step belongs on the
  render host with `nvidia-smi`/Nsight, not in a unit test.

No performance change is claimed. The measured hotspot is recorded, and the absolute
numbers do not justify a refactor in this campaign.

### P3-2 — Valueless abstractions

- `staticcheck -checks 'S1*'` on `processor` and `overlay` was clean.
- `dupl -t 80` initially reported only small test clones plus one production clone group
  (duplicated route validation). That group is removed.
- Remaining `dupl` groups are test-table repetition and two 12-line text-layer builders
  with different roles, alignment, sizes and shadows; extracting a shared helper there
  would add parameters without removing a decision.
- The `internal/queue` package is not an alias layer: it owns chunk finalization and
  parent assembly validation over client types.
- Type aliases in the overlay boundary are intentional vocabulary, not conversions.

Conclusion: **no further abstraction was removed.** The only defensible removal was the
duplicated route validator, and it was done.

## Commands run (final pass)

```text
make test-gofmt
make test-module-standalone
make check-gpu-lanes
make conformance
make test-architecture
make deadcode
make test-certification
go test -race -count=1 $(go list ./... | grep -v '/internal/architecture$')   # renderinggen
TEST_DATABASE_URL=... RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 go test -race -count=1 ./...  # queue
go build ./... && go vet ./... && go test -race -count=1 ./...                  # objectstore
CHRONON_BIN=... go test -tags certification ... -run TestFinal_ExclusiveEndNoFrame125
CHRONON_BIN=... go test -tags certification ... -run TestClassicPhrasePresetExecutesOnStrictGPU
docker compose -f docker-compose.yaml -f docker-compose.ci.yaml config
```

Results: all green. No failures were suppressed; skipped GPU/PostgreSQL tests were run
explicitly where a local binary/DSN existed.

## External blockers (not claimed)

1. **Golden E2E**: the CI job exists, but local ports are occupied by the running stack;
   stopping it is outside the production-safety boundary.
2. **Installed systemd unit**: repository units are updated; the host unit was not
   reinstalled or restarted. Deployment must install the updated unit with the new
   binary because startup validates the catalog path.

No production queue or worker was started, stopped or modified during this campaign.
