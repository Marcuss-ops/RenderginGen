# Worker image: RenderingGen + Chronon3d, versioned.
#
# Chronon3d is the single source of truth for the renderer: this image does NOT
# compile any Chronon source. The `CHRONON_RUNTIME` base image is built and
# published by the Marcuss-ops/Chronon3d repository (its own
# docker/chronon-runtime/Dockerfile) and pinned here by version tag.
#
# Build from the repo root:
#   docker build -f infra/docker/renderinggen-worker.Dockerfile \
#     --build-arg CHRONON_RUNTIME=ghcr.io/marcuss-ops/chronon3d-runtime:0.1.0 \
#     -t renderinggen-worker:0.1.0 .
#
# The Chronon3d runtime image installs the real CLI at
# /opt/chronon3d/bin/chronon3d_cli (install prefix /opt/chronon3d).
ARG CHRONON_RUNTIME=ghcr.io/marcuss-ops/chronon3d-runtime:0.1.0
FROM ${CHRONON_RUNTIME} AS runtime

# --- build the Go binary ---
FROM golang:1.25 AS builder
WORKDIR /src
COPY renderinggen/go.* ./
COPY queue/ /queue/
RUN go mod download
COPY renderinggen/ ./
ARG VERSION=0.1.0
RUN CGO_ENABLED=0 go build \
      -ldflags "-s -w -X github.com/Marcuss-ops/RenderginGen/renderinggen/internal/version.RenderingGen=${VERSION}" \
      -o /out/renderinggen ./cmd/renderinggen

# --- final image ---
FROM runtime
COPY --from=builder /out/renderinggen /usr/local/bin/renderinggen
COPY renderinggen/config.yaml /etc/renderinggen/config.yaml
COPY infra/docker/worker-entrypoint.sh /usr/local/bin/worker-entrypoint.sh

ENV CHRONON_HOME=/opt/chronon3d

# The runtime image runs as the non-root `chronon` user. Prepare the worker
# workspace before dropping privileges so it can create per-job directories
# and the daemon socket directory.
USER root
RUN mkdir -p /var/lib/renderinggen /var/run/chronon3d \
    && chown -R chronon:chronon /var/lib/renderinggen /var/run/chronon3d \
    && chmod +x /usr/local/bin/worker-entrypoint.sh
USER chronon

# The entrypoint starts the warm Chronon daemon (persistent render engine)
# and then runs the worker over IPC.
ENTRYPOINT ["/usr/local/bin/worker-entrypoint.sh"]
