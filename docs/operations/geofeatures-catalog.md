# Geographic catalog (`CHRONONTEMPLATE_CATALOG`)

Map compilation (`fly_to_feature` and `ModernMap1`) reads Natural Earth data
from a local directory. The catalog is an explicit deployment input, never a
directory discovered by walking the working directory's parents.

## Required files

| File | Content |
|---|---|
| `ne_50m_admin_0_countries.geojson` | Admin-0 countries (required) |
| `ne_10m_admin_1_gujarat.geojson` | Admin-1 regions used by the tactical fill (required) |

The repository ships a trimmed fixture at
`renderinggen/internal/geofeatures/testdata/catalog/` for tests and CI; it is
not a production basemap.

## Configuration

```sh
CHRONONTEMPLATE_CATALOG=/var/lib/renderinggen/catalog
```

- The worker loads and validates the catalog in `loadWorkerConfig`, **before
  the first claim**. A missing directory, missing file, malformed geometry or
  ambiguous feature name stops the process with a message naming the variable.
- The loaded registry is passed to the processor (`Options.GeoRegistry`) and
  from there to `overlay.CompileOptions.GeoRegistry`. The compiler does not
  call `geofeatures.DefaultRegistry()` while lowering a plan.
- The catalog is immutable for the lifetime of the process. There is no hot
  reload and no watcher: update the mount and restart the worker. This is
  deliberate — a mid-flight catalog change would make two plans of the same job
  resolve against different geometry.

## Verification

```sh
# Startup log (one line, before the first claim)
# geofeatures catalog loaded: N features from CHRONONTEMPLATE_CATALOG

# Compile a known plan from a directory unrelated to the checkout
cd "$(mktemp -d)"
CHRONONTEMPLATE_CATALOG=/var/lib/renderinggen/catalog \
  renderinggen-worker --config /etc/renderinggen/config.yaml
```

`go test ./internal/geofeatures` covers the loader; `go test ./internal/overlay
-run 'TestFlyToFeatureUsesInjectedRegistry|TestModernMapCompile'` covers the
injection contract.

## Failure and recovery

| Symptom | Cause | Action |
|---|---|---|
| `Natural Earth catalog not configured; set CHRONONTEMPLATE_CATALOG` | variable unset | set it in the unit/compose environment |
| `open Natural Earth country catalog: no such file` | mount missing or wrong path | fix the mount; the path must contain both files |
| `ambiguous feature name` | catalog contains two features with the same name/alias | use the canonical `country:`/`admin1:` selector in the plan, or fix the catalog |
| Worker booted before the mount was ready | mount ordering | restart the worker; failed loads are not cached |

For multiple replicas, mount the same catalog version everywhere and record its
checksum in the deployment manifest. A discrepancy shows up as a different map
fit between workers, not as an error.
