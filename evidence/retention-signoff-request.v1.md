# Retention/archive sign-off request — removed duplicate MP4s

**Status:** technical archive executed and verified on 7 October 2026; the owner
decision and an independent archive location are still pending. This file is a
request, not an approval.

## Scope

- Inventory: `CLEANUP_ARTIFACT_INVENTORY.csv`
- Reproducible audit: `evidence/mp4-retention-audit.v1.json`
- Audit command: `python3 scripts/audit_removed_mp4_retention.py --inventory CLEANUP_ARTIFACT_INVENTORY.csv --output evidence/mp4-retention-audit.v1.json`
- Archive command: `python3 scripts/archive_removed_mp4_duplicates.py --root . --archive <dir outside the repository> --restore-test <sha256>`
- Scope: the 30 rows classified `removed_duplicate`; this request does not
  authorize deleting any additional file.

## Verified evidence in this checkout

- All 30 files are recorded in the CSV with path, size and SHA-256.
- Their historic blobs are recoverable from the add commit recorded per item in
  the JSON report: `b412c53ae72ad788855c3bf5218d7a402016adc1`.
- Every item has at least one currently present byte-identical MP4 copy (one to
  three copies per digest; 17,898,958 bytes total across the removed rows).
- The audit probes an identical live copy for H.264 stream and positive duration
  and scans current tracked/untracked text files for exact old-path references.

## Archive executed (7 ottobre 2026)

- **Archive path:** `<local-archive-root>/mp4-duplicates-20261007`
  (outside the repository tree; exact host-local root intentionally not recorded in source control).
- Build: `scripts/archive_removed_mp4_duplicates.py`, manifest
  `manifest.v1.json` (schema `renderinggen.mp4-duplicate-archive.v1`),
  `created_at_utc: 2026-10-07T12:00:00Z`, source audit SHA-256
  `c3d1e4d8be97531883aa99bc0502f2e35a16ad2e492cd0c58db20479afd7ddf0`.
- Result: **30 distinct digests archived, 30 removed paths covered, 30/30
  archived files re-hashed and verified**, no failures. The 30 removed paths
  collapse to 30 digests, one file per digest named `<sha256>.mp4`.
- The archive also carries `evidence/CLEANUP_ARTIFACT_INVENTORY.csv` and
  `evidence/mp4-retention-audit.v1.json`, so it is self-describing without this
  repository.
- **Restore test executed:** digest
  `35e872292c99e351a00333d995821c0919b6f4ac3b11e743efa08513c2ff976e` was
  re-materialised from the archive, re-hashed (match) and probed:
  `codec_name=h264, width=1920, height=1080, duration=5.000000`.
- **This is a local archive outside the repository, not an independent or
  external archive:** it shares the host, the filesystem and the failure domain,
  so `external_archive_verified` stays `false` in the audit JSON.

## Recovery procedure (unchanged, executable)

For a given inventory row, restore bytes without relying on the live duplicate
using the `baseline_add_commit` and path in the JSON report:

```sh
git show <baseline_add_commit>:<removed_path> > <removed_path>
sha256sum <removed_path>
ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height,duration -show_entries format=duration -of json <removed_path>
```

Or from the archive: `python3 scripts/archive_removed_mp4_duplicates.py
--restore-test <sha256> --archive <dir>`, which re-copies and re-verifies.

## Owner decision required

Technically verified and filled:

- **Independent archive location (durable URI or system/path):** `PENDING` — the
  verified archive above is local; an independent/second-site location is not
  established.
- **Archive object/version or immutable identifier:** `manifest.v1.json` in the
  archive directory, digest list of 30 `sha256` entries.
- **Archive SHA-256 verification performed by / date:** the archive script
  re-hashed all 30 objects; verified 30/30 on 2026-10-07.
- **Restore test performed by / date / outcome:** archive script restore test,
  2026-10-07, hash match + H.264 1920x1080 5.000 s probe.

Still open, and only the owner can close them:

- **Accountable owner (name/team):** `PENDING`
- **Retention policy / period:** `PENDING`
- **External consumers and dynamic path construction audited by:** `PENDING`
- **Decision:** `PENDING — retain in repository until approved`
- **Approver / date:** `PENDING`

Approval must name the person/team, the retention period and (if required) an
independent archive location. A statement that copies are duplicates is not, by
itself, retention sign-off. Until the PENDING fields are completed, retain all
surviving evidence and do not remove more media.
