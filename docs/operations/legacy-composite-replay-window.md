# Legacy composite replay window — 2026-11-18

`compileLegacyCompositeImageLayers` is the only replay path for sealed plans
that predate `group_motion_id`. It is admitted only when the plan carries
`legacy_composite_replay=true`; a new plan with `image_layers` and no
`group_motion_id` fails closed instead of entering this branch.

## Census

A JSON scan of the RenderingGen, PipelineGen and BrainRust trees on 2026-11-18
found **zero** in-repo plans with a non-empty `image_layers` array (and therefore
zero legacy-shaped plans). The branch is retained only for sealed artifacts that
live outside this checkout.

## Counters

`overlay.Stats` records both paths in the same compile pass:

- `LegacyCompositeImageCount` — image_layers items lowered through the legacy
  branch;
- `LegacyCompositeReplayCount` — the subset admitted by the explicit replay
  marker.

`legacy_composite_replay_test.go` asserts both counters are `1` for a marked
plan. A fresh plan cannot increment them because the fail-closed check rejects
it first.

## Expiry

- **Owner:** RenderingGen overlay.
- **Replay window ends:** 2026-12-31.
- **Removal condition:** before that date, confirm no external sealed plan still
  needs per-child motion; then delete `compileLegacyCompositeImageLayers` and the
  replay marker, and update the contract note. Until then the path stays
  isolated and instrumented.

This is a declared compatibility window, not a second main path.
