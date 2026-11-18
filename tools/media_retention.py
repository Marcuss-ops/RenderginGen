#!/usr/bin/env python3
"""Manifest-backed retention for tracked derived artifacts.

The repository's source plates, fixtures and compact certification evidence
stay tracked. Derived render outputs are operational artifacts: this tool
hashes every tracked file under the derived roots, writes a manifest, then
(only with --apply) untracks and optionally deletes the derived classes.

The policy is split by artifact class, not by file extension. A file under a
derived root is removed unless it matches the explicit tracked allowlist
(compact JSON/Markdown/script/text evidence). Everything is hashed before any
change, and the manifest records the baseline commit, size, SHA-256 and how to
regenerate the artifact.

Usage:
    python3 tools/media_retention.py                 # dry run, writes manifest
    python3 tools/media_retention.py --apply         # git rm --cached
    python3 tools/media_retention.py --apply --delete
    python3 tools/media_retention.py --verify-manifest
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MANIFEST_DIR = ROOT / "artifacts-retention"
MANIFEST = MANIFEST_DIR / "media-retention-2026-11-18.json"
ROOTS_FILE = MANIFEST_DIR / "derived-roots.txt"
ALLOWLIST_FILE = MANIFEST_DIR / "tracked-allowlist.txt"

KEEP_PREFIXES = (
    "assets/backgrounds/",
    "testdata/",
    "evidence/",
)

REGENERATE = {
    ".mp4": "re-run the render/batch job that produced the clip",
    ".png": "re-run the render or preview command that produced the frame",
    ".jpg": "re-run the render or preview command that produced the frame",
    ".jpeg": "re-run the render or preview command that produced the frame",
    ".mp3": "re-run the audio/voiceover stage that produced the track",
    ".wav": "re-run the audio/voiceover stage that produced the track",
    ".log": "re-run the job; logs are diagnostic only",
    ".json": "re-run the job that emitted the sidecar",
    ".csv": "re-run the report/export that produced the table",
    ".tsv": "re-run the report/export that produced the table",
    ".flag": "re-run the job that emitted the flag",
}


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_lines(path: Path) -> list[str]:
    if not path.is_file():
        raise SystemExit(f"missing policy file: {path.relative_to(ROOT)}")
    return [line.strip() for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def load_roots() -> tuple[str, ...]:
    roots = tuple(line.rstrip("/") + "/" for line in read_lines(ROOTS_FILE))
    if not roots:
        raise SystemExit(f"no derived roots declared in {ROOTS_FILE.relative_to(ROOT)}")
    return roots


def load_allowlist() -> tuple[str, ...]:
    return tuple(read_lines(ALLOWLIST_FILE))


def glob_regex(pattern: str) -> re.Pattern[str]:
    """Translate the small allowlist glob dialect to a full-path regex.

    `**/` matches zero or more directories, `**` matches anything, `*` matches
    one path segment and `?` one character. This keeps `UploadDrive/**/*.json`
    matching both the root and nested metadata while never matching a PNG.
    """
    out = ["^"]
    i = 0
    while i < len(pattern):
        char = pattern[i]
        if char == "*" and pattern[i : i + 2] == "**":
            if pattern[i : i + 3] == "**/":
                out.append("(?:.*/)?")
                i += 3
                continue
            out.append(".*")
            i += 2
            continue
        if char == "*":
            out.append("[^/]*")
        elif char == "?":
            out.append("[^/]")
        else:
            out.append(re.escape(char))
        i += 1
    out.append("$")
    return re.compile("".join(out))


def allowlisted(path: str, allowlist: tuple[str, ...]) -> bool:
    return any(glob_regex(pattern).match(path) for pattern in allowlist)


def derived_root(path: str, roots: tuple[str, ...]) -> str:
    for root in roots:
        if path.startswith(root):
            return root.rstrip("/")
    return ""


def classify(path: str, roots: tuple[str, ...], allowlist: tuple[str, ...]) -> tuple[str, bool, str]:
    for prefix in KEEP_PREFIXES:
        if path.startswith(prefix):
            return "protected_source", True, f"protected prefix {prefix}"
    root = derived_root(path, roots)
    if not root:
        return "unclassified", True, "outside the declared derived roots"
    if allowlisted(path, allowlist):
        return "compact_evidence", True, "matched artifacts-retention/tracked-allowlist.txt"
    suffix = Path(path).suffix.lower()
    return "derived_render", False, REGENERATE.get(suffix, "re-run the producing job")


def candidates(roots: tuple[str, ...]) -> list[str]:
    paths = [line for line in git("ls-files", "--", *roots).splitlines() if line.strip()]
    return sorted(paths)


def build_summary(roots: tuple[str, ...], allowlist: tuple[str, ...]) -> dict[str, object]:
    entries = []
    for relative in candidates(roots):
        absolute = ROOT / relative
        if not absolute.is_file():
            raise SystemExit(f"tracked file missing from worktree: {relative}")
        kind, keep, reason = classify(relative, roots, allowlist)
        entries.append(
            {
                "path": relative,
                "class": kind,
                "keep": keep,
                "root": derived_root(relative, roots),
                "size_bytes": absolute.stat().st_size,
                "sha256": sha256_file(absolute),
                "reason": reason,
                "regenerate_with": REGENERATE.get(
                    Path(relative).suffix.lower(), "re-run the producing job"
                ),
                "deleted": False,
            }
        )
    derived = [entry for entry in entries if not entry["keep"]]
    return {
        "schema_version": 2,
        "captured_at": "2026-11-18",
        "repository": "RenderingGen",
        "head": git("rev-parse", "HEAD").strip(),
        "policy": {
            "keep_prefixes": list(KEEP_PREFIXES),
            "derived_roots": list(roots),
            "allowlist": str(ALLOWLIST_FILE.relative_to(ROOT)),
            "class_rule": "under a derived root: remove unless allowlisted",
        },
        "history_rewrite_required": True,
        "recovery": "git checkout <baseline-head> -- <path>",
        "totals": {
            "tracked_files": len(entries),
            "tracked_bytes": sum(entry["size_bytes"] for entry in entries),
            "removed_files": len(derived),
            "removed_bytes": sum(entry["size_bytes"] for entry in derived),
            "kept_files": len(entries) - len(derived),
            "kept_bytes": sum(entry["size_bytes"] for entry in entries if entry["keep"]),
        },
        "entries": entries,
    }


def write_manifest(summary: dict[str, object]) -> None:
    MANIFEST_DIR.mkdir(parents=True, exist_ok=True)
    temporary = MANIFEST.with_suffix(".json.tmp")
    temporary.write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, MANIFEST)


def verify_manifest() -> int:
    if not MANIFEST.is_file():
        print(f"FAIL missing manifest: {MANIFEST.relative_to(ROOT)}", file=sys.stderr)
        return 2
    summary = json.loads(MANIFEST.read_text(encoding="utf-8"))
    if summary.get("schema_version") != 2:
        print("FAIL manifest schema_version is not 2", file=sys.stderr)
        return 2
    failures = 0
    for entry in summary.get("entries", []):
        if entry.get("keep"):
            continue
        relative = entry["path"]
        absolute = ROOT / relative
        if entry.get("deleted"):
            if absolute.exists():
                print(f"FAIL deleted entry still present: {relative}", file=sys.stderr)
                failures += 1
            continue
        if not absolute.is_file():
            print(f"FAIL entry missing from worktree: {relative}", file=sys.stderr)
            failures += 1
            continue
        if absolute.stat().st_size != entry["size_bytes"]:
            print(f"FAIL size changed: {relative}", file=sys.stderr)
            failures += 1
            continue
        if sha256_file(absolute) != entry["sha256"]:
            print(f"FAIL sha256 changed: {relative}", file=sys.stderr)
            failures += 1
    if failures:
        print(f"FAIL {failures} manifest entries did not verify", file=sys.stderr)
        return 1
    print(f"PASS manifest verified ({summary['totals']['removed_files']} derived entries)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--apply", action="store_true", help="untrack the derived files")
    parser.add_argument("--delete", action="store_true", help="delete the derived worktree bytes")
    parser.add_argument("--verify-manifest", action="store_true", help="verify the existing manifest")
    parser.add_argument("--verbose", action="store_true", help="print every dry-run path")
    args = parser.parse_args()
    if args.delete and not args.apply:
        parser.error("--delete requires --apply")
    if args.verify_manifest:
        return verify_manifest()

    roots = load_roots()
    allowlist = load_allowlist()
    summary = build_summary(roots, allowlist)
    write_manifest(summary)
    derived = [entry for entry in summary["entries"] if not entry["keep"]]
    totals = summary["totals"]
    print(
        f"manifest: {MANIFEST.relative_to(ROOT)} "
        f"({totals['removed_files']} derived, {totals['removed_bytes']} bytes; "
        f"{totals['kept_files']} kept, {totals['kept_bytes']} bytes)"
    )

    if not args.apply:
        for entry in derived if args.verbose else []:
            print(f"DRY-RUN remove {entry['path']}")
        return 0

    # Chunk the argument list so a large repository cannot exceed ARG_MAX.
    paths = [entry["path"] for entry in derived]
    for start in range(0, len(paths), 200):
        chunk = paths[start : start + 200]
        subprocess.check_call(["git", "rm", "--cached", "--quiet", "--", *chunk], cwd=ROOT)
    print(f"untracked {len(paths)} derived files")

    if args.delete:
        for entry in derived:
            absolute = ROOT / entry["path"]
            if absolute.exists():
                absolute.unlink()
            entry["deleted"] = True
        write_manifest(summary)
        print(f"deleted {len(paths)} derived files from the worktree")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
