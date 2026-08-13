# Central object store (L3) service.
FROM golang:1.25 AS builder
WORKDIR /src
COPY objectstore/go.mod ./
RUN go mod download
COPY objectstore/ ./
RUN CGO_ENABLED=0 go build -o /out/objectstore ./cmd/objectstore

FROM scratch
COPY --from=builder /out/objectstore /usr/local/bin/objectstore
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/objectstore"]
