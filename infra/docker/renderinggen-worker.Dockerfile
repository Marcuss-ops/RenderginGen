# Worker image: RenderingGen + Chronon, versioned.
# Build from the repo root:
#   docker build -f infra/docker/renderinggen-worker.Dockerfile \
#     --build-arg CHRONON_RUNTIME=chronon-gpu-runtime:0.9.4 -t renderinggen-worker:0.1.0 .
ARG CHRONON_RUNTIME=chronon-gpu-runtime:0.9.4
FROM ${CHRONON_RUNTIME} AS runtime

# --- build the Go binary ---
FROM golang:1.25 AS builder
WORKDIR /src
COPY renderinggen/go.* ./
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

ENV CHRONON_HOME=/opt/chronon

ENTRYPOINT ["/usr/local/bin/renderinggen"]
CMD ["--config", "/etc/renderinggen/config.yaml"]
