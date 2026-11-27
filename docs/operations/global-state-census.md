# Package-level state census

RGA-12 removed the mutable package variables that the processor used as test
seams (`validateDecode`, `validateDecodeSampled`, `countDecodedFrames`). This
census records what remains, why it is allowed to be package-level, and what
would make it a defect.

The mechanical guard is
`TestProcessorTestsDoNotAssignPackageState` (`internal/architecture`): it parses
the processor package and fails if a `_test.go` file assigns any package-scope
variable. Adding a new seam therefore requires an explicit decision, not a
silent global.

## `internal/processor`

| Symbol | Kind | Owner / lifetime | Classification |
|---|---|---|---|
| `jobOutcomeHook` | `func(string)`, set by `SetJobOutcomeHook` | installed once at worker boot (`bootstrap.go`) | process-wide observability sink. Not a test seam: tests do not assign it (pinned by the scan). A second worker in one process would share the hook; acceptable because it only observes, never decides. |
| `assetDownloadClient` | `*http.Client` | lazily created, shared | HTTP connection reuse. Not mutated by tests (pinned by the scan). It carries no per-job state. |
| `ErrAssetContentMismatch`, `ErrExecutionPolicyIncompatible`, `ErrDriveCapabilityUnavailable`, `ErrDrivePublicationPermanent` | error sentinels | immutable | values, not state. |

`Processor` carries every behavior seam as an instance field (`compiler`,
`validateDecode`, `validateDecodeSampled`, `countDecodedFrames`,
`prefetchInflight`, …), installed by `NewWithOptions` and defaulted lazily for
a zero-value processor. Tests build `processorWithStubs`, which sets those
fields on the instance.

## `internal/geofeatures`

| Symbol | Kind | Classification |
|---|---|---|
| `defaultRegistry`, `defaultRegistryMu` | guarded success-only cache | process catalog. A failed load is not cached. The worker validates it at boot and injects it into `CompileOptions.GeoRegistry`; the global is no longer read while lowering a request (see `docs/operations/geofeatures-catalog.md`). |

## `internal/overlay`

The package carries ~54 `var` declarations. They are **data and catalogs**, not
seams:

- `templateRegistry`, `officialPresets`, `effectDefinitions`,
  `cameraPanSpecs`, certified map composition/camera/label sets, design tokens
  (`modernMapPaper`, `modernMapTerritory`, …) — built once at init or by a
  literal, then read.
- `chrononPluginMaps` and the `certifiedMap*` sets come from
  `mapcatalog.Embedded()` and are read-only views.
- `_ json.Marshaler` / `_ json.Unmarshaler` are compile-time interface checks.

Rule for this package: a `var` may hold immutable data; anything that decides
per-request behavior is a parameter of `CompileOptions` (camera baker,
asset sizes, geo registry) or a field of the resolved plan. No test may assign
a production `var`; if a test needs a different value, pass it in.

## `internal/chronon`

13 `var` declarations, all immutable data or interface assertions (command
defaults, renderer capability tables). No mutable test seam.

## Re-check

```sh
cd RenderingGen/renderinggen
go test -count=1 -race ./internal/processor/
go test -count=1 ./internal/architecture/
grep -rn '^var validateDecode\|^var validateDecodeSampled\|^var countDecodedFrames' --include='*.go' .
```
