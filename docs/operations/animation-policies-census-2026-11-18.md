# Animation policies census — 2026-11-18

The retired pre-v2 field `animation_policies` was searched across the source,
fixtures, evidence and the multilingual overlay corpus:

```bash
rg -l '"animation_policies"' \
  RenderingGen/evidence RenderingGen/testdata \
  RenderingGen/multilingual_overlays_v1 RenderingGen/Upload \
  RenderingGen/UploadDrive 2>/dev/null || true
```

No plan file contains the field. The only remaining matches are the retirement
test, the decoder's explicit rejection, the contract schema, and documentation.

## Replay result

There is no live plan to replay: `CompileSemantic` rejects any document carrying
`animation_policies` before lowering, with:

```
animation_policies is retired; migrate to animation groups or explicit motion_id
```

`TestRetiredAnimationPoliciesFailWithMigrationError` and
`TestPlanWithoutRetiredAnimationPoliciesStillCompiles` pin both sides of the
contract. The modern animation-group path is covered by the existing overlay
goldens.

## Counter

The legacy counter is **zero by construction**: the decoder rejects the field
before a compile pass exists, so there is no accepted legacy path that could
increment it. Adding a metric that can only ever observe a rejection would be a
second reading of the retired contract, not telemetry. The rejection test is the
executable census.

## Conclusion

No live plan uses `animation_policies`; the resolver and wire field are gone.
Ticket 07 can close without a deprecation window because the fail-closed
rejection makes the old shape unrepresentable.
