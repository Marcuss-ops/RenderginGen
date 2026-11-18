# Media retention

This directory owns the canonical record for media that leaves the Git tree.
The policy is split by artifact class, not by file extension:

| Class | Location | Rule |
| --- | --- | --- |
| Source plates | `assets/backgrounds/` | Tracked. Eleven named backgrounds only. |
| Fixtures | `testdata/` | Tracked. Small, deterministic test inputs. |
| Certification evidence | `evidence/` | Tracked. Compact proof of a visual result. |
| Derived renders | `Upload/`, `UploadDrive/`, `hdr_bokeh_focus_pull_20261108/` | Not tracked. Operational output with a TTL; recorded in the manifest and object storage. |

`tools/media_retention.py` hashes every tracked file under the registered
derived roots (media, logs, tables and flags), writes
`media-retention-2026-11-18.json`, and only then untracks/deletes the derived
classes. Compact JSON/Markdown/script/text metadata is kept by
`tracked-allowlist.txt`. The JSON manifest is the source of truth for what was
removed, its size, its SHA-256 and how to regenerate it.

The roots and the allowlist are policy files: `derived-roots.txt` and
`tracked-allowlist.txt`. `scripts/ci/check_derived_artifacts.py` fails a pull
request that tracks a file under a derived root without an allowlist match;
the `retention` workflow runs it on every PR and on `main`.

## Recovery

The manifest records the baseline `HEAD`. Before a history rewrite, any
removed path is recoverable from that commit:

```bash
git checkout <baseline-head> -- <path>
```

After a history rewrite the blob may only exist in the backup bundle or the
object-store copy; the manifest SHA-256 is the check that the recovered bytes
are the same artifact.

## What this does not do

`git rm --cached` removes files from future commits, not from existing
history. A clone of the old history still downloads the old blobs. Shrinking
the clone requires a coordinated history rewrite (`git filter-repo` or
BFG), a backup, and a force-push — an operator action that changes every
historical SHA. Do not rewrite history until the manifest and the object-store
copies are verified.
