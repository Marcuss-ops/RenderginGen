# Overlay template aliases

`legacyTemplateAliases` in `renderinggen/internal/overlay/registry.go` is the
single place that resolves the pre-migration organization/place spellings. The
registry stores only the canonical uppercase ids; aliases are normalized once at
the boundary and never become a second registry.

| Alias | Canonical template | Reason | Removal criterion |
| --- | --- | --- | --- |
| `org_default` | `ORGANIZATION_DEFAULT` | PipelineGen historically emitted the lowercase organization spelling. | No sealed plan has used it for 30 days, verified with a corpus/object-store census. |
| `gpe_default` | `LOCATION_DEFAULT` | PipelineGen historically emitted the lowercase GPE/place spelling. | No sealed plan has used it for 30 days, verified with a corpus/object-store census. |

`canonicalTemplateID` trims and lowercases only for this lookup; the canonical id
returned by the alias map stays uppercase. An unknown spelling is uppercased and
otherwise preserved, so the registry cannot invent a new template.

The aliases are deliberately kept until the census in ticket 15 is complete.
Do not remove one without checking saved plans and the PipelineGen producer
paths first.
