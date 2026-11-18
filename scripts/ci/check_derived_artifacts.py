#!/usr/bin/env python3
"""Reject tracked files under the derived roots unless explicitly allowlisted."""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RETENTION = ROOT / "artifacts-retention"
ROOTS_FILE = RETENTION / "derived-roots.txt"
ALLOWLIST_FILE = RETENTION / "tracked-allowlist.txt"


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True)


def read_lines(path: Path) -> list[str]:
    return [line.strip() for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def glob_regex(pattern: str) -> re.Pattern[str]:
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


def main() -> int:
    roots = tuple(line.rstrip("/") + "/" for line in read_lines(ROOTS_FILE))
    patterns = tuple(read_lines(ALLOWLIST_FILE))
    allow = tuple(glob_regex(pattern) for pattern in patterns)

    tracked = [line for line in git("ls-files", "--", *roots).splitlines() if line.strip()]
    added = [
        line
        for line in git("diff", "--cached", "--name-only", "--diff-filter=A", "--", *roots).splitlines()
        if line.strip()
    ]
    bad = sorted({path for path in tracked + added if not any(regex.match(path) for regex in allow)})
    if bad:
        print("FAIL tracked derived artifacts are not allowed in Git:", file=sys.stderr)
        for path in bad[:200]:
            print(f"  {path}", file=sys.stderr)
        if len(bad) > 200:
            print(f"  ... and {len(bad) - 200} more", file=sys.stderr)
        print(
            "Regenerate the artifact instead of committing it, or add an explicit "
            "compact-metadata pattern to artifacts-retention/tracked-allowlist.txt.",
            file=sys.stderr,
        )
        return 1
    print(f"PASS no unallowlisted tracked artifacts under {len(roots)} derived roots")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
