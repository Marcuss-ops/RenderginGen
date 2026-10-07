#!/usr/bin/env python3
"""Archive the removed-duplicate MP4s from the retention audit, verifiably.

The audit (evidence/mp4-retention-audit.v1.json) proves that every removed
duplicate still has a byte-identical live copy in this checkout. This script
turns that proof into a durable archive OUTSIDE the repository:

  1. one file per sha256 digest (the 30 removed paths collapse to fewer digests);
  2. every source copy is hashed before it is copied, and every archived file is
     hashed after, so a mismatch fails the run instead of producing a silently
     wrong archive;
  3. a manifest records the source copy, the removed paths that share the digest
     and the audit file digest the archive was built from;
  4. the inventory CSV, the audit JSON and the manifest are copied into the
     archive so it is self-describing without this repository;
  5. --restore-test re-materialises one archived digest and re-hashes it (and
     probes it with ffprobe when available), which is the recovery procedure in
     executable form.

A local archive outside the repository is NOT an independent/external archive:
it shares the host, the filesystem and the failure domain. The sign-off request
keeps that distinction explicit.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
from datetime import datetime, timezone

SCHEMA = "renderinggen.mp4-duplicate-archive.v1"


def sha256_file(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--root", default=".", help="repository root (default: current directory)")
    parser.add_argument("--audit", default="evidence/mp4-retention-audit.v1.json", help="retention audit JSON")
    parser.add_argument("--inventory", default="CLEANUP_ARTIFACT_INVENTORY.csv", help="inventory CSV copied into the archive")
    parser.add_argument("--archive", required=True, help="archive directory OUTSIDE the repository")
    parser.add_argument("--manifest-name", default="manifest.v1.json", help="manifest file name inside the archive")
    parser.add_argument("--restore-test", default="", help="sha256 digest to restore and re-verify after archiving")
    parser.add_argument("--restore-dir", default="", help="directory used for the restore test (default: <archive>/restore-test)")
    parser.add_argument("--created-at", default="", help="fixed UTC timestamp YYYY-MM-DDTHH:MM:SSZ for reproducible manifests")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    root = os.path.abspath(args.root)
    audit_path = os.path.join(root, args.audit)
    inventory_path = os.path.join(root, args.inventory)
    archive = os.path.abspath(args.archive)
    if archive.startswith(root + os.sep):
        print(f"refusing to write the archive inside the repository: {archive}", file=sys.stderr)
        return 1
    audit = json.load(open(audit_path, encoding="utf-8"))
    if audit.get("schema") != "renderinggen.mp4-retention-audit.v1":
        print(f"unexpected audit schema: {audit.get('schema')!r}", file=sys.stderr)
        return 1
    if not audit.get("all_have_live_byte_identical_copy"):
        print("audit does not prove a live byte-identical copy for every removed path", file=sys.stderr)
        return 1

    created_at = args.created_at or datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    os.makedirs(archive, exist_ok=True)
    evidence_dir = os.path.join(archive, "evidence")
    os.makedirs(evidence_dir, exist_ok=True)

    by_digest: dict[str, dict] = {}
    failures: list[str] = []
    for item in audit["items"]:
        digest = item["sha256"]
        entry = by_digest.setdefault(
            digest,
            {
                "sha256": digest,
                "bytes": item.get("baseline_blob_bytes", 0),
                "archived_name": f"{digest}.mp4",
                "source_copy": "",
                "removed_paths": [],
                "baseline_add_commit": item.get("baseline_add_commit", ""),
            },
        )
        entry["removed_paths"].append(item["removed_path"])
        if entry["source_copy"]:
            continue
        for candidate in item["live_byte_identical_copies"]:
            source = os.path.join(root, candidate)
            if not os.path.isfile(source):
                continue
            actual = sha256_file(source)
            if actual != digest:
                failures.append(f"{candidate}: live copy digest {actual} != audit digest {digest}")
                continue
            entry["source_copy"] = candidate
            entry["source_bytes"] = os.path.getsize(source)
            shutil.copy2(source, os.path.join(archive, entry["archived_name"]))
            break
        if not entry["source_copy"]:
            failures.append(f"{item['removed_path']}: no usable live copy found")

    for entry in by_digest.values():
        archived = os.path.join(archive, entry["archived_name"])
        if not os.path.isfile(archived):
            failures.append(f"{entry['archived_name']}: not archived")
            continue
        actual = sha256_file(archived)
        entry["verified_sha256"] = actual
        entry["verified"] = actual == entry["sha256"]
        if not entry["verified"]:
            failures.append(f"{entry['archived_name']}: archived digest {actual} != {entry['sha256']}")

    for name in (args.audit, args.inventory):
        source = os.path.join(root, name)
        if os.path.isfile(source):
            target = os.path.join(evidence_dir, os.path.basename(name))
            shutil.copy2(source, target)
            if os.path.basename(name) == os.path.basename(args.audit):
                audit_digest = sha256_file(target)
    manifest = {
        "schema": SCHEMA,
        "version": 1,
        "created_at_utc": created_at,
        "audit": {"path": args.audit, "sha256": sha256_file(audit_path)},
        "inventory": {"path": args.inventory, "sha256": sha256_file(inventory_path) if os.path.isfile(inventory_path) else ""},
        "repository_baseline_commit": audit.get("baseline_commit", ""),
        "removed_duplicate_paths": audit.get("removed_duplicate_count", 0),
        "distinct_digests": len(by_digest),
        "entries": [by_digest[digest] for digest in sorted(by_digest)],
        "external_archive": False,
        "limitations": [
            "This archive lives outside the repository but on the same host, filesystem and failure domain: it is not an independent or external archive.",
            "It preserves one byte-identical copy per digest of the removed duplicates; it does not assert legal retention, business approval or absence of external consumers.",
            "Recovery from the repository's own history remains available with the documented `git show <commit>:<path>` procedure.",
        ],
        "failures": failures,
    }
    manifest_path = os.path.join(archive, args.manifest_name)
    with open(manifest_path, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=2, sort_keys=True)
        handle.write("\n")

    restore_result = None
    if args.restore_test:
        digest = args.restore_test
        entry = by_digest.get(digest)
        if entry is None:
            print(f"restore test digest {digest} is not part of the archive", file=sys.stderr)
            failures.append(f"restore test digest {digest} not archived")
        else:
            restore_dir = args.restore_dir or os.path.join(archive, "restore-test")
            os.makedirs(restore_dir, exist_ok=True)
            restored = os.path.join(restore_dir, entry["archived_name"])
            shutil.copy2(os.path.join(archive, entry["archived_name"]), restored)
            restored_digest = sha256_file(restored)
            probe = ""
            if shutil.which("ffprobe"):
                completed = subprocess.run(
                    ["ffprobe", "-v", "error", "-select_streams", "v:0",
                     "-show_entries", "stream=codec_name,width,height,duration",
                     "-of", "default=noprint_wrappers=1", restored],
                    capture_output=True, text=True, check=False,
                )
                probe = completed.stdout.strip()
            restore_result = {
                "sha256": digest,
                "restored_to": os.path.relpath(restored, archive),
                "restored_sha256": restored_digest,
                "verified": restored_digest == digest,
                "ffprobe": probe,
            }
            if not restore_result["verified"]:
                failures.append(f"restore test mismatch for {digest}")
            manifest["restore_test"] = restore_result
            with open(manifest_path, "w", encoding="utf-8") as handle:
                json.dump(manifest, handle, indent=2, sort_keys=True)
                handle.write("\n")

    print(json.dumps({
        "archive": archive,
        "manifest": manifest_path,
        "audit_sha256": manifest["audit"]["sha256"],
        "distinct_digests": len(by_digest),
        "removed_duplicate_paths": manifest["removed_duplicate_paths"],
        "verified": sum(1 for entry in by_digest.values() if entry.get("verified")),
        "restore_test": restore_result,
        "failures": failures,
    }, indent=2))
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
