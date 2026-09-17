# Central object store (L3) service.
FROM golang:1.25 AS builder
WORKDIR /src
COPY objectstore/go.mod ./
RUN go mod download
COPY objectstore/ ./
# Build identity (internal/buildinfo). These three ARGs are stamped into
# symbols that actually exist, so the published image can prove which revision
# and bytes it runs on its own /health — the question the store, alone among
# the three services, used to be unable to answer.
ARG VERSION=0.1.0
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 go build \
      -ldflags "-s -w -X github.com/Marcuss-ops/RenderingGen/objectstore/internal/buildinfo.Version=${VERSION} -X github.com/Marcuss-ops/RenderingGen/objectstore/internal/buildinfo.GitCommit=${COMMIT} -X github.com/Marcuss-ops/RenderingGen/objectstore/internal/buildinfo.BuildTime=${BUILD_TIME}" \
      -o /out/objectstore ./cmd/objectstore

FROM scratch
COPY --from=builder /out/objectstore /usr/local/bin/objectstore
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/objectstore"]
