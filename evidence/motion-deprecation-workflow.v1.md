# Motion deprecation workflow (audit-only until approval)

This workflow is deliberately non-destructive. The audit command does not modify the canonical ChrononTemplate catalog, does not mark a production motion as deprecated, and does not remove/replace registry entries.

## 1. Inventory and proposal

Create a proposal conforming to `contracts/motion-deprecation-proposal.v1.schema.json` with:

- the canonical motion ID and a distinct registered replacement;
- reason, accountable owner, required targets and exact consumer paths;
- `deprecated_on`, `remove_after` and a compatibility window of at least 30 days.

Run from the `renderinggen` module, using a fixed date for reproducible evidence:

```sh
go run ./cmd/motion-deprecation-audit \
  -proposal ../evidence/<proposal>.v1.json \
  -root .. \
  -audit-date YYYY-MM-DD \
  -output ../evidence/<proposal>-audit.v1.json
```

The report conforms to `contracts/motion-deprecation-audit-report.v1.schema.json`. Exit 0 means `PASS_AUDIT_ONLY`; exit 2 means a valid audit was generated but its gate is `BLOCKED`; exit 1 means input/schema/registry/report validation or I/O failed. Preserve the report even for exit 2 and inspect the consumer deltas.

## 2. Interpret the consumer audit

The scanner compares literal references in the checkout with proposal-declared paths. Resolve every undeclared or missing path and repeat the audit after updating the proposal. This static scan is only a repository signal: it cannot establish absence of dynamically assembled IDs, external clients, ignored files or deployed saved plans. Obtain those inventories from their owners before changing catalog state.

`PASS_AUDIT_ONLY` means the declared consumers and required target compatibility match at this audit date, and the minimum window is still open. It is not approval, certification or authority to change production metadata.

## 3. Approved deprecation and migration window

After product and owning-team approval, the catalog owner may publish deprecation metadata in the canonical catalog and regenerate the embedded artifact through the documented catalog sync. During the compatibility window:

1. Keep `Resolve(motion_id)` working so existing saved plans still compile and render through `remove_after`.
2. Exclude deprecated IDs from new picker/selectable projections immediately and publish the removal date and human-readable reason.
3. At and after `remove_after`, compiler validation fails deterministically for a plan that still names the retired ID; it never substitutes a similar motion.
4. Update every in-repository consumer and request external consumers to migrate to the replacement explicitly. Never rewrite saved plans automatically to a merely similar motion.
5. Re-run the audit after consumer changes and retain each report with the proposal.
6. Require current compiler/unit/schema/conformance gates and the applicable owner review before advancing the date.

The isolated registry API `MarkDeprecated` and `Selectable`/`SelectableCategoryMotionIDs` tests exercise this transition in a local registry only. No production registry state is mutated by this procedure or by the fixture in this repository.

## 4. Removal gate

After `remove_after` has passed, removal requires all of the following: zero known saved-plan or package consumers, external consumer confirmation, an explicit versioning/migration decision, owner sign-off, and a reviewed plan for deterministic errors on retired IDs. Keep historical IDs resolvable for the agreed migration/rollback horizon. If any condition is unknown, retain the ID and report the item as blocked.

## Evidence included

- `evidence/motion-deprecation-fixture.v1.json` is a fixture using an ID/replacement that remain live in production; its future dates are test-only.
- `evidence/motion-deprecation-fixture-report.v1.json` is an audit-only PASS generated with a fixed date; it does not represent a production deprecation.
- `renderinggen/internal/motion/deprecation_test.go` proves a deprecated item is hidden from a selectable projection but remains resolvable in an isolated registry.
- `renderinggen/internal/overlay/deprecation_catalog_test.go` proves the runtime picker omits deprecated IDs.

Do not deprecate a production motion on the basis of this fixture: current test and catalog consumers are intentionally present, and an external product owner must approve any public compatibility window.
