#!/usr/bin/env python3
"""Read-only evidence report for duplicate MP4s already absent from the worktree.

Usage from the RenderingGen repository root:
  python3 scripts/audit_removed_mp4_retention.py \
    --inventory CLEANUP_ARTIFACT_INVENTORY.csv \
    --output evidence/mp4-retention-audit.v1.json
  python3 scripts/audit_removed_mp4_retention.py --self-test

This tool never restores, deletes, uploads, or modifies media. Git history is
used only to hash the baseline blobs; a matching live copy proves byte identity,
not external retention approval or that a producer never references old names.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

SCHEMA = "renderinggen.mp4-retention-audit.v1"
EXPECTED = 30
IGNORED_REFERENCE_FILES = {
    "CLEANUP_ARTIFACT_INVENTORY.csv",
    "CLEANUP.md",
    "cleanupplan2.0.md",
}


def run_git(root: Path, *args: str) -> bytes:
    result = subprocess.run(["git", *args], cwd=root, check=True, stdout=subprocess.PIPE)
    return result.stdout


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def tracked_files(root: Path) -> list[str]:
    return [p.decode("utf-8", "surrogateescape") for p in run_git(root, "ls-files", "-z").split(b"\0") if p]


def ignored_scan_path(path: Path) -> bool:
    return ".git" in path.parts or ".tmp" in path.parts or "__pycache__" in path.parts


def adding_commit(root: Path, rel: str) -> str:
    commits = run_git(root, "log", "--all", "--diff-filter=A", "--format=%H", "--", rel).decode().splitlines()
    if not commits:
        raise ValueError(f"no available Git add-commit found for removed path: {rel}")
    return commits[0]


def present_mp4_hashes(root: Path) -> dict[str, list[str]]:
    result: dict[str, list[str]] = {}
    for rel in tracked_files(root):
        if not rel.lower().endswith(".mp4"):
            continue
        path = root / rel
        if not path.is_file():
            continue
        digest = sha256(path.read_bytes())
        result.setdefault(digest, []).append(rel)
    # Include untracked present MP4s too: an archive/copy need not be tracked.
    for path in root.rglob("*.mp4"):
        if ignored_scan_path(path) or not path.is_file():
            continue
        rel = path.relative_to(root).as_posix()
        if rel in {item for paths in result.values() for item in paths}:
            continue
        digest = sha256(path.read_bytes())
        result.setdefault(digest, []).append(rel)
    for paths in result.values():
        paths.sort()
    return result


def reference_hits(root: Path, files: list[str], removed_path: str) -> list[str]:
    hits: list[str] = []
    needle = removed_path.encode("utf-8")
    text_suffixes = {".go", ".py", ".sh", ".md", ".json", ".yaml", ".yml", ".toml", ".csv", ".txt", ".xml", ".html", ".js", ".ts", ".tsx", ".jsx"}
    for rel in files:
        if rel in IGNORED_REFERENCE_FILES or rel == removed_path or rel.endswith("/scripts/audit_removed_mp4_retention.py"):
            continue
        path = root / rel
        if path.suffix.lower() not in text_suffixes or not path.is_file():
            continue
        try:
            data = path.read_bytes()
        except OSError:
            continue
        if is_retention_report(data):
            continue
        if b"\0" not in data and needle in data:
            hits.append(rel)
    # Include untracked text too; an uncommitted consumer is still a worktree consumer.
    for path in root.rglob("*"):
        if ignored_scan_path(path) or not path.is_file() or path.suffix.lower() not in text_suffixes:
            continue
        rel = path.relative_to(root).as_posix()
        if rel in files or rel in IGNORED_REFERENCE_FILES or rel == removed_path or rel.endswith("/scripts/audit_removed_mp4_retention.py"):
            continue
        try:
            data = path.read_bytes()
        except OSError:
            continue
        if is_retention_report(data):
            continue
        if b"\0" not in data and needle in data:
            hits.append(rel)
    return sorted(set(hits))


def is_retention_report(data: bytes) -> bool:
    try:
        return json.loads(data).get("schema") == SCHEMA
    except (json.JSONDecodeError, AttributeError):
        return False


def probe(path: Path) -> dict[str, object]:
    command = [
        "ffprobe", "-v", "error", "-select_streams", "v:0",
        "-show_entries", "stream=codec_name,width,height,duration",
        "-show_entries", "format=duration", "-of", "json", str(path),
    ]
    completed = subprocess.run(command, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    doc = json.loads(completed.stdout)
    streams = doc.get("streams", [])
    if len(streams) != 1:
        raise ValueError(f"expected exactly one selected video stream, got {len(streams)}")
    stream = streams[0]
    duration = float(stream.get("duration") or doc.get("format", {}).get("duration") or 0)
    if stream.get("codec_name") != "h264" or duration <= 0:
        raise ValueError(f"unexpected video stream facts: {stream}")
    return {
        "status": "pass", "codec": stream["codec_name"],
        "width": stream.get("width"), "height": stream.get("height"),
        "duration_seconds": duration,
    }


def audit(root: Path, inventory_path: Path, output_path: Path | None = None) -> dict[str, object]:
    with inventory_path.open(newline="", encoding="utf-8") as handle:
        rows = [row for row in csv.DictReader(handle) if row["working_tree_status"] == "removed_duplicate"]
    if len(rows) != EXPECTED:
        raise ValueError(f"inventory has {len(rows)} removed_duplicate rows, expected {EXPECTED}")
    files = tracked_files(root)
    live_by_hash = present_mp4_hashes(root)
    results = []
    all_recoverable = True
    all_unreferenced = True
    for row in rows:
        rel = row["path"]
        expected_sha = row["sha256"]
        source_commit = adding_commit(root, rel)
        baseline = run_git(root, "cat-file", "blob", f"{source_commit}:{rel}")
        baseline_sha = sha256(baseline)
        if baseline_sha != expected_sha:
            raise ValueError(f"baseline blob hash disagrees with inventory: {rel}")
        matches = [candidate for candidate in live_by_hash.get(expected_sha, []) if candidate != rel]
        if not matches:
            all_recoverable = False
        hits = reference_hits(root, files, rel)
        if output_path is not None:
            report_path = output_path.resolve()
            if report_path.is_relative_to(root.resolve()):
                report_rel = report_path.relative_to(root.resolve()).as_posix()
                hits = [hit for hit in hits if hit != report_rel]
        if hits:
            all_unreferenced = False
        live_probe = probe(root / matches[0]) if matches else {"status": "unavailable"}
        results.append({
            "removed_path": rel,
            "sha256": expected_sha,
            "baseline_add_commit": source_commit,
            "baseline_blob_sha256_matches_inventory": True,
            "baseline_blob_bytes": len(baseline),
            "live_byte_identical_copies": matches,
            "live_copy_count": len(matches),
            "removed_path_absent": not (root / rel).exists(),
            "current_textual_references": hits,
            "probe_of_identical_live_copy": live_probe,
            "restore_from_git": f"git show {source_commit}:{rel} > {rel}",
        })
    commit = run_git(root, "rev-parse", "HEAD").decode().strip()
    return {
        "schema": SCHEMA,
        "version": 1,
        "baseline_commit": commit,
        "inventory": inventory_path.relative_to(root).as_posix(),
        "removed_duplicate_count": len(results),
        "all_baseline_blob_hashes_match_inventory": True,
        "all_have_live_byte_identical_copy": all_recoverable,
        "all_removed_paths_absent": all(row["removed_path_absent"] for row in results),
        "all_paths_have_no_current_textual_reference": all_unreferenced,
        "external_archive_verified": False,
        "retention_sign_off": "pending_external_owner_and_archive_location",
        "evidence_limitations": [
            "Byte-identical live copies prove recoverability from this checkout only, not an independent archive.",
            "Text search covers current tracked text files and exact removed-path strings; it cannot prove absence of dynamically constructed or external consumers.",
            "No deletion was performed by this audit; each baseline blob comes from its historical add-commit (the current HEAD does not contain the removed path).",
            "Retention and archive sign-off require a named owner and independently verified storage location.",
        ],
        "items": results,
    }


def self_test() -> None:
    assert sha256(b"abc") == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / "clip.mp4").write_bytes(b"bytes")
        assert sha256((root / "clip.mp4").read_bytes()) == sha256(b"bytes")
    print("self-test OK: SHA-256 and read-only fixture checks")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", default="CLEANUP_ARTIFACT_INVENTORY.csv")
    parser.add_argument("--output", default="evidence/mp4-retention-audit.v1.json")
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    root = Path.cwd().resolve()
    inventory = (root / args.inventory).resolve()
    output = (root / args.output).resolve()
    doc = audit(root, inventory, output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(doc, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"audit: {doc['removed_duplicate_count']} removed MP4s; copies={doc['all_have_live_byte_identical_copy']}; references_clear={doc['all_paths_have_no_current_textual_reference']}; sign_off={doc['retention_sign_off']}")
    print(f"wrote {output.relative_to(root)}")
    return 0 if doc["all_baseline_blob_hashes_match_inventory"] else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, subprocess.CalledProcessError, json.JSONDecodeError) as error:
        print(f"audit_removed_mp4_retention: {error}", file=sys.stderr)
        raise SystemExit(1)
