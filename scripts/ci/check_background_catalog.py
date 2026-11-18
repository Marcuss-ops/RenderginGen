#!/usr/bin/env python3
"""Keep the tracked background count and the retention README in sync.

The source plates are the only background media allowed in Git. A new tracked
`.mp4` under `assets/backgrounds/` must be accompanied by an updated README;
otherwise the catalog silently grows beyond the documented eleven plates.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
README = ROOT / "artifacts-retention" / "README.md"
EXPECTED = 11
DECLARATION = "Eleven named backgrounds only"


def main() -> int:
    tracked = [
        line
        for line in subprocess.check_output(
            ["git", "ls-files", "assets/backgrounds/*.mp4"], cwd=ROOT, text=True
        ).splitlines()
        if line.strip()
    ]
    if len(tracked) != EXPECTED:
        print(
            f"FAIL tracked backgrounds = {len(tracked)}, want {EXPECTED}: "
            "update the catalog/README deliberately, not by drift",
            file=sys.stderr,
        )
        for path in tracked:
            print(f"  {path}", file=sys.stderr)
        return 1

    readme = README.read_text(encoding="utf-8")
    if DECLARATION not in readme:
        print(
            f"FAIL {README.relative_to(ROOT)} must state {DECLARATION!r}",
            file=sys.stderr,
        )
        return 1

    print(f"PASS {len(tracked)} tracked backgrounds match the README declaration")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
