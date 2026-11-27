# Job authority matrix

This is a map of the fact owners across job generation and rendering. The
representations belong to different phases; a serialized copy is safe only
when it carries the authority's value or acts as a rebuildable projection.

| Fact | Authority | Writers and persisted form | Readers / projections | Evidence and current limit |
|---|---|---|---|---|
| Editorial intent and timeline | PipelineGen | PipelineGen compilers; semantic overlay plan and compiled final-job plan | RenderingGen prepares the execution plan; final-job worker executes the compiled plan | `final_job_payload_builder.go` builds and seals one plan, serializes once, and uses its SHA for the attempt idempotency key. The digest identifies submitted plan content; it is not the rendered artifact SHA. |
| Prepared assets and compiled geometry/motion | RenderingGen | `PrepareJob` and the prepared render package; content-addressed package and canonical plan identity | Render session and Chronon request | `prepared_render_package_test.go` pins content addressing, reuse, and identity drift rejection; `prepare_compile_count_test.go` pins one resolve/lower for an ordinary job. |
| Durable execution demand (GPU/backend/fallback/encoder constraints) | Queue job | Queue submission resolves legacy input once into `Job.ExecutionPolicy`; repository persists it | RenderingGen's pure `ResolveExecutionPolicy` checks demand against worker config and plan, then projects the immutable result to Chronon | `queue/client/execution_policy_test.go`, `queue/internal/repository/postgres/execution_policy_integration_test.go`, and processor `gpu_execution_policy_test.go` / `execution_policy_worker_lane_test.go`. A plan cannot silently relax incompatible durable demand. |
| PipelineGen render cache identity | PipelineGen render plan | Sealed `RenderExecutionPolicy` contributes to `PlanSHA256` and cache/checkpoint identity | PipelineGen render/cache path | This is a different contract from Queue's hardware admission policy: stream-copy eligibility and renderer/encoder/profile hashes versus GPU worker admission. Keep the distinction explicit; do not treat the two structs as interchangeable. |
| Verification requirement and verdict | Queue policy is the durable request; worker receipt is execution evidence | Queue stores the requested verification policy; Chronon receipt records resolved policy and proof; Queue artifact ledger stores the verdict | Worker certification gate and downstream artifact consumers | Processor policy/proof matrix (`store_artifact_verification_test.go`, `receipt_verify_test.go`) and Queue ledger round-trip (`artifact_verification_test.go`). RGA-06 still tracks full-worker E2E and alerting. |
| Backend actually used | Chronon receipt plus RenderingGen certification | Chronon reports execution facts; RenderingGen publishes the certified backend on the artifact | Queue ledger and downstream consumers | `published_backend_contract_test.go` checks PipelineGen's enum when the sibling checkout is available and enforces the strict-native gate. Standalone CI visibly skips the cross-repo pin. |
| Rendered byte identity | SHA-256 of the output bytes | RenderingGen verifies local bytes and object-store key before writing the artifact reference | Queue ledger and artifact readers | `pipeline_full_test.go` and `publish_invariants_test.go` pin local/store/ledger identity; staged and receipt identity have dedicated tests. |
| Artifact bytes | Object store | RenderingGen publishes content-addressed bytes after certification gates (stream staging may write earlier) | Consumers only resolve a queue-ledger reference | `artifact-lifecycle.md`; RGA-05 order, staging, and GC tests. CAS identity alone does not grant consumption. |
| Terminal state, attempts, lease, retry | Queue PostgreSQL | Queue `Complete` / `Fail` / `Cancel` under lease generation | Worker claim loop and status readers | `repository_test.go`, `cancel_test.go`, `lease_generation_test.go`, `worker_death_test.go`; only Queue transitions terminal state. |
| Worker-local artifact mirror | No authority; diagnostic projection | RenderingGen's optional SQLite recorder | Local diagnostics only | `record_artifact.go` states mirror write failures are non-fatal; Queue completion remains authoritative. |

## Operating rule

When adding a job field, name the fact it represents and assign its owner here.
If another phase needs a copy, define it as either a validated request or a
rebuildable projection, then pin drift with a test at the boundary. Do not
collapse plan identity, execution admission, receipt evidence, artifact hash,
and queue state into one generic “job identity”.
