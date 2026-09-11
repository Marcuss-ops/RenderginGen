# Boundary conformance gate

This repository ships an **executable** architecture gate that makes the
PipelineGen / RenderingGen / Chronon3d boundary drift architecturally impossible
to reintroduce. It is not documentation: it fails CI.

## The four non-negotiable boundaries

```text
PipelineGen   owns semantic intent     →  renderinggen.overlay-plan.v1
RenderingGen  owns visual lowering     →  chronon.render-plan.v2
Chronon       owns physical execution  →  one versioned plan format
Queue/Storage own one wire contract each
```

- PipelineGen never emits a concrete Chronon plan. It submits the semantic
  `renderinggen.overlay-plan.v1` document (kind, text, timing, entity_ref,
  asset_refs, `preset_id`).
- RenderingGen is the only semantic compiler. It resolves template, preset,
  font asset, geometry and motion, and is the sole producer of
  `chronon.render-plan.v2`.
- Chronon receives only concrete geometry/style/animation. The render-plan
  layer has **no** `preset` field (`additionalProperties: false`); the physical
  output is always `chronon.render-plan.v2`.

## What the gate forbids

`renderinggen/internal/architecture/conformance.go` scans the RenderingGen
repository and, when present, its `refactored/` (PipelineGen) and `Chronon3d/`
siblings. Each rule maps to one deleted contract:

| Rule | Forbidden reintroduction |
|---|---|
| `render_plan_v1_schema` | `chronon.render-plan.v1` |
| `render_plan_unversioned_schema` | the unversioned `"chronon.render-plan"` literal |
| `module_path_typo` | the `RenderginGen` module-path typo |
| `hardcoded_home_path` | a hardcoded developer home path in Go test/source |
| `template_alias_org_default` | the dead `ORG_DEFAULT` alias (canonical: `ORGANIZATION_DEFAULT`) |
| `template_alias_gpe_default` | the dead `GPE_DEFAULT` alias (canonical: `LOCATION_DEFAULT`) |
| `entity_template_inference` | classifying entity/phrase/word from `template_id` instead of `kind` |
| `semantic_stats_second_pass` | a second stats interpretation outside the compile pass |
| `partnn_filename` | `*_partNN.go` manual-splitting files |
| `legacy_layer_preset_field` | a bare `json:"preset"` slot on the render-plan layer |

## Run it

```bash
cd renderinggen && go test ./internal/architecture/... -count=1
# or, from the repository root:
make test-architecture
```

CI runs it automatically: the `test-renderinggen` job executes
`go test ./...` inside `renderinggen`, which includes the gate.

The gate also validates the production path end to end:

```bash
cd renderinggen && go test ./internal/overlay/ -run TestBoundaryContract -count=1
```

That test takes one fixture per supported `kind × template`, validates the input
against `contracts/overlay-plan.v1.schema.json`, compiles it through the single
compiler, and validates the output against
`Chronon3d/schemas/json/chronon.render-plan.v2.schema.json`.

## Ratchet baseline

Pre-existing violations are recorded in
`renderinggen/internal/architecture/testdata/conformance-baseline.txt`
(`<rule>|<repo-relative path>`). The ledger is a **ratchet**:

- A **new** occurrence fails the gate; fix it, never baseline it.
- A baselined occurrence that is fixed becomes **stale** and fails the gate until
  its ledger line is removed.
- Entries for a sibling repository that is not checked out are ignored, so the
  gate is correct in a standalone RenderingGen checkout.

After fixing violations, shrink the ledger explicitly:

```bash
make refresh-conformance-baseline
```

Never run it to hide a new violation.

## Current baseline

The remaining ledger entries are pre-existing, tracked work:

- `partnn_filename` — `*_partNN.go` files across the three repositories, to be
  renamed by responsibility.
- `render_plan_v1_schema` / `render_plan_unversioned_schema` — the legacy
  PipelineGen v1 compiler (`refactored/internal/capabilities/overlays/chronon.go`)
  and its golden matrix, plus the Chronon3d SDK-consumer fixtures.
- `legacy_layer_preset_field` — the same legacy v1 layer model.

These are the targets of the legacy-contract demolition; the gate guarantees
they cannot grow while that work proceeds.
