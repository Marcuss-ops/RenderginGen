# RenderingGen audit — measurements

Companion to `00-INDEX.md`. Every number here was produced from the working tree on
27 November 2026, not copied from the 23 November package-complexity report.

Tools used: `gocyclo`, `dupl` and `staticcheck` from the Go binary directory on `PATH`;
`gocognit` v1.2.0 installed separately for this audit. The report records the tools and
versions rather than a developer-specific absolute path.

## 1. Size and shape of `internal/overlay`

| Metric | Value |
|---|---:|
| Non-test Go files | 95 |
| Test Go files | 164 |
| Production LOC | 24,007 |
| Test LOC | 31,278 |
| Exported top-level funcs + types | 823 (707 funcs, 116 types) |

Commands:

```sh
find internal/overlay -name '*.go' ! -name '*_test.go' | wc -l
find internal/overlay -name '*_test.go' | wc -l
find internal/overlay -name '*.go' ! -name '*_test.go' -print0 | xargs -0 cat | wc -l
find internal/overlay -name '*_test.go' -print0 | xargs -0 cat | wc -l
grep -rhE '^func [A-Z][A-Za-z0-9_]*\(' internal/overlay/*.go | wc -l
grep -rhE '^type [A-Z][A-Za-z0-9_]* ' internal/overlay/*.go | wc -l
```

Context: `overlay` (95) + `processor` (46) hold 141 of the repository's 341 non-test Go
files, about 41%. That concentration is structural, not by itself a defect.

## 2. Fan-in and fan-out

Computed from `go list -f '{{.ImportPath}}|{{range .Imports}}{{.}} {{end}}' ./...`,
counting only intra-module imports.

| Package | Fan-in | Fan-out |
|---|---:|---:|
| `internal/overlay` | 11 | 12 |
| `internal/chronon` | 9 | 1 |
| `internal/processor` | 1 | 13 |
| `cmd/renderinggen` | 0 | 19 |
| `internal/vramprobe` | 1 | 5 |
| `internal/tacticalfill` | 2 | 4 |

`internal/overlay` imports:

```text
countryflags
internal/clampx
internal/geo
internal/geofeatures
internal/mapgeom
internal/motion
internal/motionmath
internal/numeric
internal/overlay/mapcatalog
internal/tacticalfill
motion-certification
motion-preview
```

Interpretation: overlay is a high fan-in authority (11 packages consume it) and a moderate
fan-out compiler. Its coupling is mostly downward into pure utilities (`geo`, `mapgeom`,
`motion`, `numeric`), except `tacticalfill`, which is a presentation helper. The next
extraction candidates are pure responsibilities with no compiler state (typography
geometry, map path math); splitting the semantic compiler itself would create two
authorities.

## 3. Churn (90 days)

Churn is counted as **file-name occurrences** in `git log --name-only`, not commits: one
commit touching five overlay files contributes five. History starts 7 October 2026.

| Path | File touches (90 d) | Top file |
|---|---:|---|
| `internal/overlay/` | 1,592 | `compiler.go` 73 |
| `internal/processor/` | 622 | `pipeline.go` 54 |
| `internal/chronon/` | 260 | — |
| `internal/motion/` | 240 | — |
| `internal/media/` | 87 | — |
| `cmd/renderinggen/` | 81 | — |

Top overlay files:

```text
73  compiler.go
69  semantic_compile.go
42  motion.go
34  official_presets.go
29  semantic_image_compile.go
```

Command shape:

```sh
git log --since='90 days ago' --name-only --pretty=format: -- internal/overlay \
  | grep '^renderinggen/internal/overlay/' | wc -l
```

## 4. Cyclomatic complexity (production only)

`gocyclo` over `internal/overlay/*.go`, test files excluded:

```text
functions=759 avg=6.73
```

| Function | CC |
|---|---:|
| `validateMapLODs` | 45 |
| `validateMapPresentationParams` | 44 |
| `applyShapeProceduralParams` | 40 |
| `resolveChainSegment` | 36 |
| `applyFrameTreatment` | 36 |
| `validateMapContract` | 35 (was 48) |
| `parseImageBackdrop` | 34 |
| `resolveTextSpecBase` | 33 |

The only change in this campaign is `validateMapContract`: the duplicated route
validation block moved to `validateMapRouteEntries`, taking it from 48 to 35. The map
validators remain the top of the distribution because they are the contract surface; no
further split was made just to lower the number.

## 5. Cognitive complexity (production only)

`gocognit -top 100 internal/overlay`, test files excluded:

| Function | Cognitive |
|---|---:|
| `FitImageLayersToSizes` | 56 |
| `validateMapPresentationParams` | 54 |
| `applyChartParams` | 53 |
| `resolveTextSpecGeometry` | 52 |
| `evaluateCameraSamples` | 48 |
| `validateTextRuntimeOverrides` | 47 |
| `applyShapeProceduralParams` | 46 |
| `applyDecodedTextRuntimeOverrides` | 46 |

`validateMapContract` is no longer in the top eight. Test functions have much higher
numbers (up to 124); they are not production risk and were not refactored.

## 6. Benchmarks

Environment: Go 1.25.9, 24 logical CPUs, Linux. Absolute numbers are noisy under load;
the shape and allocation counts are the useful part.

| Benchmark | Time/op | Bytes/op | Allocs/op |
|---|---:|---:|---:|
| `CompileSemantic` | 1.641 ms | 207,016 | 2,833 |
| `CompileSemanticChain` | 2.089 ms | 208,270 | 2,466 |
| `CompileSemanticModernMap` | 4.511 ms | 829,447 | 2,266 |
| `OverlayCompileSemantic` (processor) | 626 µs | 83,934 | 762 |
| `PrepareJob` | 5.77–8.06 ms | 619–751 KB | 29,831–31,363 |
| `HashioSHA256` (256 MiB) | 2.96 s | — | 17 (90.65 MB/s) |
| `SimplifyRing` | 34.87 ms | 3,368,696 | 163 |
| `DecimateRing` | 4.94 µs | 8,192 | 1 |
| `RingsBounds` | 79.1 µs | 0 | 0 |
| `Retime` | 4.07 µs | 0 | 0 |
| `SamplePropertyTrack` | 170 µs | 50,432 | 2,979 |

`PrepareJob` varies with cache/GC state across runs; the range is reported instead of a
single cherry-picked value.

Allocation profile for `CompileSemanticModernMap` (`-memprofile`, `pprof -alloc_space`):

```text
125.23 MB  48.06%  modernMapPathGeometry
 57.62 MB  22.11%  mapgeom.SimplifyPolyline
  8.04 MB   3.09%  geofeatures.(*Registry).Rings
  7.51 MB   2.88%  mapgeom.SimplifyRing
```

Actionable? Not in this campaign. `modernMapPathGeometry` dominates allocated bytes, but
the complete plan compiles in 4.5 ms and the allocations are short-lived. Changing it now
would be optimization without a measured end-to-end consumer cost.

## 7. GPU and VRAM

Device: NVIDIA RTX A4000, 16,376 MiB, driver-visible baseline 3,572 MiB used.

Measured during `TestClassicPhrasePresetExecutesOnStrictGPU` (1280×720, 2 s, Vulkan
backend, NVENC, native encoder, `require_gpu_native`):

| Metric | Value |
|---|---:|
| Baseline GPU memory | 3,572 MiB |
| Peak GPU memory | 4,207 MiB |
| Delta | +635 MiB |
| Render wall time | 3.00 s |

Sampling command:

```sh
nvidia-smi --query-gpu=memory.used --format=csv,noheader,nounits -lms 200
```

Limitations: `nvidia-smi` measures device-wide used memory, not this process alone, and
does not report fragmentation. There is no per-frame host↔device transfer measurement in
this campaign. Those require a dedicated profiling run on the render host (Nsight
Systems / CUPTI), not a unit test. The software-backend certification render (125 frames,
11.12 s) exercised the full decode/encode path without a GPU.

## 8. Duplication and static analysis

`dupl -t 80` on `internal/overlay/*.go`:

- before the cleanup: four groups, one of them a 20-line production route validator
  duplicated between `semantic_map.go` and `semantic_map_modern_validation.go`;
- after the cleanup: four groups remain, all either test-table repetition or two small
  text-layer builders (`modernMapLabelLayer` / `modernMapCreditLayer`) that differ in role,
  alignment, size, fill and shadow.

`staticcheck -checks 'S1*'` on `internal/processor` and `internal/overlay`: clean. No
simplification finding was suppressed.

Conclusion: no additional abstraction was removed. The remaining text-layer similarity is
below the threshold where a shared helper removes a decision; it would only add parameters.
