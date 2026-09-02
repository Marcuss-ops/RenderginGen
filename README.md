# RenderingGen

GPU overlay rendering worker, containerized so a worker can run anywhere
(local GPU, Runpod, Lambda GPU, bare metal) **without changing PipelineGen**.

## Architecture

```
PipelineGen -> Central Queue -> RenderingGen workers (same image)
                                   -> Artifact Storage (L3)
                                      + local cache (L2 NVMe) + VRAM (L1)
```

RenderingGen never knows about the provider (Runpod, local, ...). It only
receives config: queue endpoint, artifact store, Chronon backend, GPU device.

Workers **pull** jobs from the central queue (claim -> render -> complete/fail);
they are never pushed HTTP requests directly.

## Repository layout

- `renderinggen/` — Go worker (queue client, storage/cache, GPU detect, health)
- `queue/` — central pull-based job queue (claim + lease expiry); HTTP contract documented in [`queue/README.md`](queue/README.md)
- `contracts/` — JSON Schemas for the cross-service wire contracts (`renderinggen.job.v1`)
- `objectstore/` — central object storage (L3) used by the worker cache
- `infra/docker/` — `renderinggen-worker` image
- `.github/workflows/` — CI: build, test, publish versioned images on `main`

## Overlay contract and renderer

PipelineGen emits the semantic `renderinggen.overlay-plan.v1` contract. The
RenderingGen worker validates and compiles that plan locally into Chronon's
`chronon.render-plan.v2`, materializes its content-addressed assets, and only
then invokes Chronon3d. Concrete Chronon plans are not accepted on the production semantic job path; RenderingGen owns the only lowering chain.

## Curated background library

`assets/backgrounds/` contains six normalized background videos supplied from
the Drive references used for visual overlay certification. They are
content-addressed in [`assets/backgrounds/manifest.json`](assets/backgrounds/manifest.json)
and are ready for the semantic `VIDEO_BACKGROUND` template. The checked-in
bytes are video-only: the source AAC tracks were removed so a background can
never replace or contaminate PipelineGen's master voiceover.

Before enqueueing a production job, upload the selected file bytes to the
artifact store under the manifest SHA-256 and reference that hash in
`asset_refs`. RenderingGen will materialize the file and Chronon will use it
as the full-canvas video source while important phrases, words, images and
entity cards remain independent layers.

## Renderer

Chronon3d is the **single source of truth** for the renderer. RenderingGen does
not vendor or compile any Chronon source — there is no `COPY chronon/` in any
RenderingGen Dockerfile. The worker image is built `FROM
chronon3d-runtime:<version>`, an image produced by the `Marcuss-ops/Chronon3d`
repository:

```
Marcuss-ops/Chronon3d ──CI──▶ chronon3d-runtime:<version>
                                    │
                              RenderingGen worker
                                    │
                           renderinggen-worker:<version>
```

Chronon3d's `docker-runtime.yml` workflow builds its own
`docker/chronon-runtime/Dockerfile` and publishes
`ghcr.io/marcuss-ops/chronon3d-runtime:<version>`, which installs the real CLI
at `/opt/chronon3d/bin/chronon3d_cli`. RenderingGen consumes that image via the
`CHRONON_RUNTIME` build arg — it never builds Chronon itself.

The two projects are versioned independently:

- `RenderingGen` — worker orchestration (queue, storage, workspace)
- `Chronon3d` — render engine, consumed as a pinned runtime image

## Images

- `renderinggen-worker:<version>` — `FROM chronon3d-runtime:<version>` + Go binary + config

Always use versioned tags, never `latest`.

## Database

The central queue persists jobs, attempts, artifacts, workers, events and
metrics to PostgreSQL (the source of truth); binary artifacts stay in the
object store. Schema migrations live in `queue/migrations/` and are applied
automatically at startup when `DATABASE_URL` is set — the in-memory store
remains the default for local/dev without a database.

`infra/docker/docker-compose.yaml` runs PostgreSQL and wires `DATABASE_URL`
for the queue service.

## Build & run

```sh
# worker (local)
cd renderinggen
go build ./cmd/renderinggen
```

Images are built in two independent steps — first the renderer runtime from the
real Chronon3d repo, then the worker on top of it:

```sh
# 1. Build the chronon3d-runtime image from the Chronon3d repository root:
cd ../Chronon3d
docker build -f docker/chronon-runtime/Dockerfile -t chronon3d-runtime:0.1.0 .

# 2. Build the worker image (RenderingGen repo root), FROM that runtime:
cd ../RenderingGen
docker build -f infra/docker/renderinggen-worker.Dockerfile \
  --build-arg CHRONON_RUNTIME=chronon3d-runtime:0.1.0 \
  -t renderinggen-worker:0.1.0 .
```

In CI the runtime image is published by Chronon3d's `docker-runtime.yml`, so the
worker workflow just pins `CHRONON_RUNTIME` to the published tag
(`ghcr.io/marcuss-ops/chronon3d-runtime:0.1.0`).

## End-to-end (CLI)

The full CLI path — queue → worker → materialize → `plan.json` → real
`chronon3d_cli` (software backend) → `result.mp4` → artifact store →
completed — is covered by two integration tests that run against the real
binary and skip when it is absent:

- `renderinggen/internal/chronon/chronon_integration_test.go` — renders the
  asset-free `ExampleColorSmokePlan` through the real CLI.
- `renderinggen/internal/processor/processor_integration_test.go` — runs the
  whole processor pipeline against the real CLI and verifies the published
  artifact.

Point them at an install prefix with `CHRONON_HOME` (default
`/opt/chronon3d`):

```sh
CHRONON_HOME=/opt/chronon3d go test ./internal/chronon ./internal/processor -run 'Integration|EndToEnd' -v
```

To exercise the loop over the real services, start the stack and run the
smoke script (submits a self-contained color job, polls for completion,
downloads the artifact):

```sh
cd infra/docker && docker compose up --build -d
../e2e/run-e2e.sh
```

### Golden canary (permanent regression gate)

`GoldenOverlayJobV1` (`testdata/golden/golden-overlay-job-v1.json`) is the
**frozen golden job** for the real workload — 1280×720 @ 30fps, 5 seconds
(150 frames), with a background image, an important phrase
(`title_centered`), an important word (`kinetic_word`) and an image overlay.
The fixtures are deterministic (`infra/e2e/gen-golden-assets.py`) and their
SHA-256 hashes are baked into the payload; `golden_test.go` locks the payload,
the fixtures and the Go constant together, so any drift fails at unit-test
time.

The canary (`infra/e2e/run-golden-overlay.sh`) certifies the **whole real
chain**: queue submit → worker claim → asset materialization → `plan.json` →
real `chronon3d_cli` → artifact store → queue completion → download → ffprobe
verification (1280×720, 30fps, ~5s, hash-identical download), plus:

- **PostgreSQL persistence** — `render_jobs` (state=completed,
  attempt_count=1), exactly one `render_attempt`, exactly one
  `JOB_CREATED`/`JOB_CLAIMED`/`JOB_COMPLETED` event, and the
  `render_artifacts` row whose `sha256` matches the downloaded bytes.
- **Idempotent replay without a new render** — re-submitting the byte-identical
  job resolves to the existing job (HTTP 200/409), returns the same artifact
  hash, and leaves attempts and render events unchanged.

Run it with a single command (builds the runtime image from `../Chronon3d`
on first use, boots the stack, runs the canary, tears the stack down):

```sh
make golden-e2e
```

Reset the canary's persisted state (needed after intentional golden drift)
with `make golden-e2e-reset`. The canary also runs in CI on every push to
`main` (`.github/workflows/build.yaml` → `golden-e2e`).

## Health

`GET /health` returns versioned metadata:

```json
{
  "worker": "renderinggen-77",
  "renderinggen": "0.1.0",
  "chronon": "0.9.4",
  "overlay_schema": 3,
  "backend": "software",
  "status": "ready"
}
```
