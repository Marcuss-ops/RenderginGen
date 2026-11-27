# GPU, lease and worker-loss fault matrix

The queue owns the execution policy (`GPU_ONLY`, `GPU_PREFERRED`, `CPU_ONLY`)
and the lease/fencing tokens; the worker applies them and never re-derives
them. This matrix is the contract for what happens when the GPU, the daemon or
the worker disappears. Each row names the test that pins it; do not duplicate a
scenario in a second test.

| # | Scenario | Expected outcome | Test |
|---|---|---|---|
| 1 | `GPU_ONLY` claimed, device gone before execution | job fails with a capability error; **no** software fallback; no artifact | `TestClaimEnforcesExecutionPolicyOnPostgres`, `TestResolveExecutionPolicyMatrix` |
| 2 | `GPU_PREFERRED`, device lost mid-render | exactly one software fallback; one artifact; `backend=software`; `chronon_software_fallback=1` | `TestExecuteRenderSessionPublishesSoftwareFallback` |
| 3 | `GPU_ONLY`, device lost mid-render | explicit failure, no published output, lease released or expired; **never** a software completion | `TestExecuteRenderSessionGPUOnlyFailsClosedWhenTheDeviceDisappears`, `TestExecuteRenderSessionRefusesFallbackWhenPolicyForbidsIt` |
| 4 | worker registered with GPU, device removed | it stops proving GPU capability and no longer receives `GPU_ONLY` work | `TestCapabilityDowngradeRevokesGPUAdmission` |
| 5 | Chronon daemon reset between two jobs | reconnect succeeds, no orphan process, next job renders without duplicate output | `TestExecuteRenderSessionRecoversAfterADaemonReset` |
| 6 | lease expires during publication, second worker claims | first worker's `Rendered` is rejected by `lease_generation`; only the current generation can publish | `TestLeaseGenerationFencesStaleTransitions` |
| 7 | worker killed after publish, before `Complete` | re-claim of `RENDERED` hydrates the same artifact; stale `Complete` fenced; one artifact row and one `JOB_COMPLETED` | `TestWorkerDeathBetweenPublishAndCompleteLeavesOneLedger` |

## Signals

| Fact | Signal |
|---|---|
| software fallback happened | metric `chronon_software_fallback` (per-job metrics) and `chronon_job_gpu_software_fallback_frames_delta` |
| GPU lane failed | job `FAILED` with the renderer error; `run_gpu` phase error in the worker log |
| lease fenced | queue error on the transition (`lease generation …`); no event is appended for the stale attempt |
| lease recovery | `JOB_REQUEUED` event written by `RequeueExpired` / recovery sweep |
| completion | exactly one `JOB_COMPLETED` event per job |

There is no separate "fencing counter": a fenced transition returns an error to
the caller and writes no state, which is observable in the worker log and in
the absence of the event. Adding a counter would require a queue-side metrics
surface that does not exist today; the event stream is the authority.

## Running the matrix

```sh
cd RenderingGen/renderinggen
go test -count=1 -race -run 'TestExecuteRenderSession|TestResolveExecutionPolicy' ./internal/processor/

cd ../queue
TEST_DATABASE_URL='postgres://renderinggen:renderinggen@127.0.0.1:5432/renderinggen?sslmode=disable' \
RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 \
go test -race -count=1 -run 'TestLease|TestClaim|TestCapability|TestWorkerDeath' -v ./internal/repository/postgres/
```

Scenarios 6–7 require PostgreSQL: fencing and lease expiry are properties of the
SQL transaction, and a memory repository cannot prove them.

## Operational response

- **Device lost / `GPU_ONLY` failure**: do not re-queue on a CPU worker to
  "save" the job. Restore the device, restart the daemon, and let the lease
  recovery re-claim it. A `GPU_ONLY` job completed by a software backend is a
  contract violation, not a degraded success.
- **Capability downgrade**: after a GPU reset, wait for the worker's heartbeat
  to register the capability again; a worker that cannot prove a GPU is
  correctly invisible to `GPU_ONLY` jobs.
- **Lease fenced**: the stale worker must discard its result. If it logs a
  publish that the queue rejected, the object is an unreferenced CAS object —
  see `artifact-lifecycle.md`.
- **Worker killed after publish**: re-claim is safe. Keep the artifact address
  stable on the retry; the ledger row is written once by the current generation.

## Real-GPU lane

There is no default real-GPU fault-injection target in `Makefile`: the CI lane
has no NVIDIA device. A manual run can exercise scenario 3 by starting a short
render and resetting the device; record the `renderinggen` log and the queue
events. This is opt-in and is not part of `make verify`.
