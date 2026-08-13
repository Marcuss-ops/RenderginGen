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
- `chronon/` — native Vulkan renderer (C++), versioned via `chronon/VERSION`
- `queue/` — central pull-based job queue (claim + lease expiry)
- `infra/docker/` — `chronon-gpu-runtime` base image + `renderinggen-worker`
- `.github/workflows/` — CI: build, test, publish versioned images on `main`

## Images

- `chronon-gpu-runtime:<version>` — Chronon binary, libs, shaders, Vulkan runtime
- `renderinggen-worker:<version>` — `FROM chronon-gpu-runtime` + Go binary + config

Always use versioned tags, never `latest`.

## Build & run

```sh
# worker (local)
cd renderinggen
go build ./cmd/renderinggen

# images (from repo root)
docker build -f infra/docker/chronon-gpu-runtime.Dockerfile \
  -t chronon-gpu-runtime:0.9.4 .
docker build -f infra/docker/renderinggen-worker.Dockerfile \
  -t renderinggen-worker:0.1.0 .
```

## Health

`GET /health` returns versioned metadata:

```json
{
  "worker": "renderinggen-77",
  "renderinggen": "0.1.0",
  "chronon": "0.9.4",
  "overlay_schema": 3,
  "backend": "vulkan",
  "status": "ready"
}
```
