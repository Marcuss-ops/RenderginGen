# RenderingGen structural and performance baseline — 2026-11-27

Measured on the local checkout (`main` at `b9135bf` plus the audit-fix working
tree), Go 1.25.9, Linux amd64, Intel Xeon E3-12xx (Ivy Bridge). These are
**current baselines**, not before/after claims: the per-job claim path and the
old composition order no longer exist in this tree, so there is no honest
"before" to compare against without checking out `e8fec015`. Every number below
was produced by the command shown and can be reproduced.

## Code shape (RGA-09, RGA-14)

| Fact | Value |
|---|---|
| `internal/overlay` non-test files / lines | 95 / 24,040 |
| `internal/overlay` test files | 166 |
| Largest non-test file | `semantic_map.go`, 712 lines |
| Next largest | `semantic_effects.go` 686, `semantic_contract_types.go` 680, `runtime_style.go` 672, `selection_model.go` 669 |
| Packages importing `internal/overlay` | 13 (7 `cmd/*`, `processor`, `renderbatch`, `architecture`, `version`, `vramprobe`, `webphrase`) |
| Commits touching the three modules in the last 30 days | 85 |

Most-changed files in the last 30 days:

| Changes | File |
|---:|---|
| 17 | `internal/overlay/compiler.go` |
| 16 | `internal/overlay/semantic_compile.go` |
| 12 | `internal/overlay/text_spec.go` |
| 11 | `internal/overlay/semantic_image_compile.go` |
| 10 | `internal/processor/prepare_stages.go` |
| 10 | `internal/processor/gpu_run.go` |
| 10 | `internal/overlay/semantic_map_modern.go` |

Reading: the package is large by line count but not by file size — no file
exceeds ~700 lines, and the churn is concentrated in the compiler entry points
rather than spread across the 95 files. The extraction candidate is therefore
the **pure lowering helpers** (`text_spec.go`, camera math, geometry), not a
mechanical split by size. No split was performed in this change: moving 24k
lines without a measured post-move profile would be a rename, not a refactor.
Proposed threshold for reopening: a single non-test file above 1,000 lines, or
an import cycle attempted between the extracted package and `overlay`.

## Compile-path cost (RGA-16)

```sh
cd RenderingGen/renderinggen
CHRONONTEMPLATE_CATALOG="$PWD/internal/geofeatures/testdata/catalog" \
  go test -run '^$' -bench 'BenchmarkCompileSemantic' -benchmem -benchtime=20x ./internal/overlay/
```

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `CompileSemantic` | 1,999,309 | 210,569 | 2,834 |
| `CompileSemanticChain` | 1,712,641 | 212,041 | 2,467 |
| `CompileSemanticModernMap` | 3,707,926 | 827,942 | 2,263 |

ModernMap compiles ~1.9x slower than the text path and allocates ~3.9x the
bytes. The extra cost is the Natural Earth fit and the plate geometry; it is the
first candidate for a cache keyed by `(place, canvas, lod window)`, but only
after a profile shows the fit dominating (not measured here: `pprof` on the
benchmark is the next step, not a guess).

## Claim hydration (RGA-13)

```sh
cd RenderingGen/queue
TEST_DATABASE_URL=… RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 \
  go test -run '^$' -bench 'BenchmarkClaimBatchHydration' -benchmem -benchtime=50x \
  ./internal/repository/postgres/
```

| Batch | ns/op | B/op | allocs/op | SQL statements |
|---:|---:|---:|---:|---:|
| 1 | 2,192,151 | 11,689 | 175 | 3 |
| 16 | 2,303,663 | 58,016 | 419 | 3 |
| 64 | 3,074,490 | 221,977 | 1,159 | 3 |

The statement count is constant, proven by
`TestAttachClaimArtifactsUsesConstantStatements` (1, 16 and 64 jobs all issue
exactly 3 reads). Latency grows ~1.4x from 1 to 64 jobs because the per-job
`claimCandidateJob` writes remain O(n) by design; the read side is flat.

## Backend parity (RGA-10)

The shared suite `queue/internal/repository/repositorytest/suite.go` runs the
same scenarios against the memory and PostgreSQL repositories. It has 7
top-level scenarios; the PostgreSQL package additionally runs the atomicity,
fencing, policy and concurrency tests. Full run:

```sh
cd RenderingGen/queue
TEST_DATABASE_URL=… RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 go test -race -count=1 ./...
```

Result on 2026-11-27: all packages green (postgres 22.1 s). The suite is the
parity authority; a behavior added to only one backend is a test the other
backend fails, not a documentation gap.

## SSOT inventory (RGA-11)

A job exists in six representations. Each has exactly one owner; the table is
the checklist for any change that adds a field:

| Representation | Owner | Pinned by |
|---|---|---|
| `renderinggen.job.v1` submission JSON | `queue/client` / producer | job schema tests |
| `model.Job` (queue-internal) | `queue/internal/model` | shared repository suite |
| `render_jobs` row + JSONB columns | `queue/migrations` | PostgreSQL repository tests |
| `renderinggen.overlay-plan.v1` | PipelineGen producer, `overlay` consumer | decode/contract tests |
| `chronon.render-plan.v2` | `overlay` lowering | golden semantic/v2 tests |
| receipt + `render_artifacts` | Chronon → `processor` → ledger | `receipt_verify_test.go`, artifact round-trip |

No duplication was resolved in this change. The remaining SSOT risk is the
artifact metadata (storage key, hash, size, provenance): it is written by the
worker, mirrored in SQLite, and re-read by the queue. `TestArtifactPersistedOnComplete`
and the claim-hydration tests prove the PostgreSQL copy; the SQLite mirror has
its own tests.

## Primitive obsession / wrappers (RGA-15, RGA-17)

No production change in this pass: the audit's examples (`string` for content
addresses, sizes, policies) are already centralized where the invariants live
(`store.CanonicalContentAddress`, `overlay.CompileOptions`, execution-policy
enum). The mechanical dead-export gate (`deadcode -test ./...`) and the
architecture tests remain the enforcement. Introducing typed wrappers for
`SizeBytes`/`SHA256` would touch every wire struct; it needs a separate measured
proposal, not a cleanup bundled into this wave.

## Limits

- No GPU was available: GPU/VRAM profiling (RGA-16) and the real-engine
  certification lane were not run. `RENDERINGGEN_SKIP_GPU_E2E=1` was explicit.
- No `pprof` capture was taken; the compile benchmarks give cost, not a hotspot.
- CI runs on GitHub were not executed from this workstation (no push). The
  local commands mirror `.github/workflows/build.yaml`; the workflow itself was
  updated with the catalog env, media/catalog preflight and the anti-skip guard.
