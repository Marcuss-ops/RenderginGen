#!/usr/bin/env python3
"""Manifest-backed retention migration for tracked MP4 artifacts.

The repository's source plates, fixtures and compact certification evidence
stay tracked. Derived render outputs are operational artifacts: this tool
hashes every tracked MP4, writes the manifest, then (only with --apply)
untracks and optionally deletes the derived classes.

Because the manifest is written before any change, and the baseline commit
still contains the bytes, an accidental deletion is recoverable with:

    git checkout HEAD -- <path>

Usage:
    python3 tools/media_retention.py            # dry run, writes no changes
    python3 tools/media_retention.py --apply    # git rm --cached derived files
    python3 tools/media_retention.py --apply --delete  # also remove worktree bytes
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MANIFEST_DIR = ROOT / "artifacts-retention"
MANIFEST = MANIFEST_DIR / "media-retention-2026-11-11.json"

KEEP_PREFIXES = (
    "assets/backgrounds/",
    "testdata/",
    "evidence/",
)
DERIVED_PREFIXES = (
    "Upload/",
    "UploadDrive/",
    "hdr_bokeh_focus_pull_20261108/",
)


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def classify(path: str) -> tuple[str, bool]:
    if path.startswith("assets/backgrounds/"):
        return "source_plate", True
    if path.startswith("testdata/"):
        return "fixture", True
    if path.startswith("evidence/"):
        return "certification_evidence", True
    if path.startswith(DERIVED_PREFIXES):
        return "derived_render", False
    return "unclassified", True


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--apply", action="store_true", help="untrack the derived files")
    parser.add_argument("--delete", action="store_true", help="delete the derived worktree bytes")
    args = parser.parse_args()
    if args.delete and not args.apply:
        parser.error("--delete requires --apply")

    paths = [line for line in git("ls-files", "*.mp4").splitlines() if line.strip()]
    if not paths:
        print("no tracked MP4 files")
        return 0

    entries = []
    derived = []
    for relative in paths:
        absolute = ROOT / relative
        if not absolute.is_file():
            print(f"tracked file missing from worktree: {relative}", file=sys.stderr)
            return 2
        kind, keep = classify(relative)
        entry = {
            "path": relative,
            "class": kind,
            "keep": keep,
            "size_bytes": absolute.stat().st_size,
            "sha256": sha256_file(absolute),
        }
        entries.append(entry)
        if not keep:
            derived.append(relative)

    summary = {
        "captured_at": "2026-11-11",
        "repository": "RenderingGen",
        "head": git("rev-parse", "HEAD").strip(),
        "policy": {
            "keep": list(KEEP_PREFIXES),
            "remove": list(DERIVED_PREFIXES),
        },
        "history_rewrite_required": True,
        "recovery": "git checkout <baseline-head> -- <path>",
        "totals": {
            "tracked_files": len(entries),
            "tracked_bytes": sum(entry["size_bytes"] for entry in entries),
            "removed_files": len(derived),
            "removed_bytes": sum(
                entry["size_bytes"] for entry in entries if not entry["keep"]
            ),
        },
        "entries": entries,
    }

    MANIFEST_DIR.mkdir(parents=True, exist_ok=True)
    temporary = MANIFEST.with_suffix(".json.tmp")
    temporary.write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, MANIFEST)
    print(
        f"manifest: {MANIFEST.relative_to(ROOT)} "
        f"({summary['totals']['removed_files']} derived, "
        f"{summary['totals']['removed_bytes']} bytes)"
    )

    if not args.apply:
        for relative in derived:
            print(f"DRY-RUN remove {relative}")
        return 0

    # Chunk the argument list so a large repository cannot exceed ARG_MAX.
    for start in range(0, len(derived), 200):
        chunk = derived[start : start + 200]
        subprocess.check_call(["git", "rm", "--cached", "--quiet", "--", *chunk], cwd=ROOT)
    print(f"untracked {len(derived)} derived MP4 files")
    if args.delete:
        for relative in derived:
            absolute = ROOT / relative
            if absolute.exists():
                absolute.unlink()
        print(f"deleted {len(derived)} derived MP4 files from the worktree")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
