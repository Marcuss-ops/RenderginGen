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
| `hardcoded_home_path` | a hardcoded developer home path in any source or configuration carrier (not only Go) |
| `template_alias_org_default` | the dead `ORG_DEFAULT` alias (canonical: `ORGANIZATION_DEFAULT`) |
| `template_alias_gpe_default` | the dead `GPE_DEFAULT` alias (canonical: `LOCATION_DEFAULT`) |
| `entity_template_inference` | classifying entity/phrase/word from `template_id` instead of `kind` |
| `semantic_stats_second_pass` | a second stats interpretation outside the compile pass |
| `partnn_filename` | `*_partNN.*` manual-splitting files (any scanned carrier, including shell) |
| `legacy_layer_preset_field` | a bare `json:"preset"` slot on the render-plan layer |

This table is a checked projection of `Rules()`, not an independent copy:
`TestConformanceDocListsEveryRule` fails if the two rule-id sets differ in
either direction. Rules match the *unescaped* projection of each line, because
a shell script embedding a JSON document (`\"chronon.render-plan\"`) otherwise
hides the marker behind its backslash — `TestRulesCatchEscapedCarriers` is the
regression for exactly that evasion.

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

It needs the sibling `Chronon3d/` schema, which lives outside this repository,
so the test **skips** (never fails) in a standalone RenderingGen checkout where
`go.work` and the siblings are absent. The contract is verified where the
workspace exists; in the standalone case the absence stays visible as a skip
instead of a false pass or a false failure.

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

The ledger file is the only source of truth for what is still baselined; this
section describes it rather than enumerating it, so it cannot drift away from
the file. `TestBaselineRulesAreKnown` fails on a ledger line whose rule no
longer exists (an immortal entry), and a repo-local entry whose file was
deleted is reported stale (previously only an absent sibling repository was
ignored).

**The ledger is currently empty.** Every rule above is enforced with zero
exemptions: the legacy unversioned `chronon.render-plan` producers, the legacy
v1 layer model, the machine-specific paths and the manual-splitting files have
all been removed from the tree, so their ledger lines were ratcheted away.
The rules stay in the gate so the deleted shapes cannot come back.

A rule with no ledger line and no violation is a *live* rule; a rule with no
ledger line but a surviving occurrence fails the gate as a NEW violation. There
is no third state.
