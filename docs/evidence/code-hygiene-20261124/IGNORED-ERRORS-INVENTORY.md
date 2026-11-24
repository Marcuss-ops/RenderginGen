# Ignored-error inventory — RenderingGen, 2026-11-24

**Scope:** production Go files in `renderinggen/`, `queue/`, and `objectstore/`.
Test files are excluded: their 62 `_ =` sites are fixture and temp-directory
cleanup, not production behaviour.

**Method:**

```sh
rg -n '^\s*_ =' renderinggen queue objectstore --glob '*.go' --glob '!*_test.go'
```

The command found **109** production sites at the start of this pass. The ticket's
108 was an audit snapshot; the command is the reproducible count. After the
changes below, **66** sites remain, all class (a).

## Classification

- **(a) best-effort** — a failure cannot change the returned result, the
  persisted artifact, or the job state. Each remaining group has a comment
  explaining why discarding it is safe.
- **(b) propagate** — a failure changes the result and must be returned to the
  caller.
- **(c) log** — a failure is diagnostic/observability and must be visible.

## Settled in this pass (43 sites)

| Area | Count | Resolution |
|---|---:|---|
| `internal/workspace/workspace.go` | 24 | Inline close/unlock sites consolidated into `releaseStaleCandidate`; `Cleanup()` now propagates registry unlock/close failures; constructor teardown rationale documented. |
| HTTP response encoders | 9 | `health` (3), `queue/internal/server` (1) and `objectstore/internal/server` (5) now log encode failures. |
| Prefetch/plan diagnostics | 4 | `worker_pools` hand-off cleanup (3) and `plan_dump` write (1) now log failures. |
| Storage durability diagnostics | 2 | `l2_write` parent-directory sync/close failures are logged instead of disappearing. |
| CLI diagnostics | 1 | `chainpancheck` reports marshal/write failures for its plan dump. |
| Dead or duplicate cleanup | 3 | `semantic_text` dead constant reference removed; `canonical_encode` redundant pre-remove calls removed. |

The `plan_dump` path also had a real call-site bug: `materialize.go` passed
`ws.Root()` where the function expects a job ID. It now passes `job.ID`, so the
dump lands under the job name the diagnostic promises.

No class **(b)** site remains. The poison state write in
`queue/internal/repository/postgres/decode.go` already returns its error; the
attempt/event history written after it is deliberately secondary.

## Remaining sites (66)

| File | Count | Class | Why discarding is safe |
|---|---:|---|---|
| `renderinggen/internal/workspace/workspace.go` | 20 | (a) | Constructor/teardown close and unlock after a primary error; the returned error names the operation that failed. Success-path `Cleanup()` propagates its cleanup errors. |
| `renderinggen/internal/processor/prescale_images.go` | 7 | (a) | Cache warm-up and temp-file cleanup. The render already owns the bytes in memory; the next job pays the cold path. |
| `renderinggen/internal/storage/storage.go` | 5 | (a) | Closing the input on an open/read/write error path; the primary error is returned. |
| `renderinggen/internal/chronon/daemon_pool.go` | 4 | (a) | Killing a daemon that already failed to answer; `wait`/reap remains authoritative. |
| `objectstore/internal/store/staging.go` | 4 | (a) | Aborting a rejected pending object or removing its advisory sidecar; the content-address mismatch is the caller's result. |
| `renderinggen/internal/workspace/workspace_lock_windows.go` | 3 | (a) | `CloseHandle` on a mutex that was never published; the wait error/result is returned. |
| `renderinggen/internal/storage/staging_client.go` | 3 | (a) | Closing the source on a stat/type error, and closing an already-drained response body. |
| `renderinggen/internal/chronon/telemetry.go` | 3 | (a) | Optional schema/legacy-timing projection; the typed receipt or bounded summary has already been validated. |
| `queue/internal/repository/postgres/decode.go` | 3 | (a) | Attempt/event history after the poison state transition; the state write itself is returned. |
| `queue/cmd/queued/main.go` | 3 | (a) | Forced close after the shutdown deadline, and connection close inside the reconnect loop. |
| `renderinggen/internal/workspace/workspace_lock_other.go` | 2 | (a) | Removing our own just-created marker after a failed write/close; the write/close error is returned. |
| `renderinggen/internal/media/gop_probe.go` | 2 | (a) | `Wait` is required to reap a canceled ffprobe child; its error is not the probe result. |
| `renderinggen/internal/artifactdb/sqlite.go` | 2 | (a) | Rollback after a DDL/stamp failure; the migration error is returned. |
| `renderinggen/internal/workspace/workspace_lock_marker.go` | 1 | (a) | Removing a marker already renamed out of the live name; cleanup failure cannot resurrect the lock. |
| `renderinggen/internal/processor/canonical_receipt.go` | 1 | (a) | Removing an unpublished temp receipt after a rename failure; the rename error is returned. |
| `renderinggen/internal/processor/builtin_fonts.go` | 1 | (a) | Removing a partial target after a write/close failure; the staging error is returned. |
| `renderinggen/internal/health/health.go` | 1 | (a) | HTTP shutdown error after the server has already stopped; `Run` returns the listener result. |
| `renderinggen/internal/config/config_defaults.go` | 1 | (a) | Removing the write-probe temp file after the directory already failed the probe. |

## Verification

- `make verify` → exit 0: gofmt, standalone builds, deadcode (only the four
  documented external-contract exports), `go vet` and `test-unit` on
  `renderinggen`, `queue` and `objectstore`.
- `go test -race ./internal/processor -run 'TestPrefetch'` → ok.
- `go test -race ./internal/overlay -run 'TestEntityStyleAlias|TestEntityStyleRegistry'` → ok.
- `go test -race ./internal/media -run 'TestBinariesResolution'` → ok.
- `go test -race ./internal/repository/postgres -run 'TestNormalizeJobType'` → ok.
- `gofmt -l renderinggen queue objectstore` → empty.
