# Choosing render verification policy

The worker applies one configured policy per render (`pipeline.receipt_verify`,
also accepted as `RENDERINGGEN_RECEIPT_VERIFY`). Chronon records the resolved
wire policy in the media receipt; the queue artifact records the worker's
verification verdict. `strict` is worker-only and requests Chronon's
`certify` tier while retaining the fail-closed rule.

| Policy | Decode guarantee | Overlay composition evidence | Missing evidence |
|---|---|---|---|
| `fast` | Container and stream checks; no full decode promise | Not promised | Tolerated and recorded |
| `sample` | Deterministic sample; escalates to full decode on doubt | Not promised | Tolerated and recorded |
| `normal` | One full decode, by Chronon or the worker post-lane gate | Not promised | Composition is recorded `unverifiable` |
| `certify` | Full decode evidence | Required when the plan authors an overlay | Fails before publication |
| `strict` | Same fail-closed behavior as `certify` | Required when the plan authors an overlay | Fails before publication |

A plan without an authored overlay has no composition claim to prove; its
composition verdict is `not_required`, including under `certify` and `strict`.
Do not treat `unverifiable` as `verified` when consuming the artifact ledger.
The verification block and per-artifact metrics are persisted with the Queue
artifact, so diagnosis does not depend on worker logs.

## Retry behavior

A failure for missing or divergent evidence is a failed attempt, never a
completed artifact. The Queue owns retry count and lease. A retry must execute
the gates again and produce new evidence before `Complete` can write the
ledger. Do not manually change the artifact verdict or mark the job complete.

## Monitoring hook

The worker emits `composition_prediction_unverifiable` per artifact and stores
it with artifact processing metrics. Alerting should be configured by the
runtime that exports Queue metrics; this repository does not define the
production metrics backend or alert-rule format. A rising count means the
summary needed to compare authored overlays with the actual execution path is
missing or unreadable. Pair it with `composition_prediction_divergence`, which
means the summary was readable and contradicted the plan.
