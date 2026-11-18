# Telemetry sidecar retirement — 2026-11-18

## Inventory

A read-only scan of the local artifact roots found:

```text
134  *.timing.json
  0  *.telemetry-summary.json
```

The timing files are the v2/v3 sidecars consumed by
`ReadTimingSidecarTelemetry`. No retained artifact still carries the legacy
`chronon3d.render-telemetry-summary.v1` sidecar.

## Decision

The v1 reader (`TelemetrySummarySchema`, `ReadTelemetrySummaryFile` and the
legacy suffix) was removed. `ReadTelemetrySummary` now accepts only the timing
sidecar and rejects a v1 file with an explicit error. This is a one-release
window: v2/v3 are supported; v1 is retired immediately because the inventory is
empty.

A `chronon_telemetry_schema_read_total` counter is therefore not useful: there
is no accepted v1 path left to count. The rejection test and this inventory are
the evidence. Reintroducing a v1 compatibility reader would require a new
decision and a new counter.

## Replay

There is no v1 artifact to replay. The timing-sidecar tests cover acceptance of
v2/v3 and rejection of v1:

```bash
cd RenderingGen/renderinggen
go test -count=1 ./internal/chronon -run 'Telemetry|Timing'
```

`internal/processor` is intentionally not part of this sidecar decision; its
own gates are separate.
