#!/usr/bin/env python3
"""Repository-local check for RenderingGen worker GPU lane profile contracts."""

from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
PROFILE_GROUPS = {
    "native": [ROOT / "infra/native/renderinggen-native.yaml", ROOT / "infra/native/renderinggen-b.yaml", ROOT / "renderinggen/config.yaml"],
    "docker": [ROOT / "infra/docker/worker-config.yaml", ROOT / "infra/docker/worker-config-b.yaml", ROOT / "infra/docker/worker-config-drive.yaml"],
    "ci": [ROOT / "infra/docker/worker-config-ci.yaml"],
}
EXPECTED = {"native": 3, "docker": 2, "ci": 1}


def value_for(path: Path, key: str) -> int:
    text = path.read_text()
    match = re.search(rf"(?m)^\s*{re.escape(key)}:\s*(\d+)\s*(?:#.*)?$", text)
    if not match:
        raise ValueError(f"{path.relative_to(ROOT)}: missing integer {key}")
    return int(match.group(1))


def main() -> int:
    failures = []
    for group, paths in PROFILE_GROUPS.items():
        wanted = EXPECTED[group]
        for path in paths:
            try:
                lanes = value_for(path, "gpu_lanes")
                workers = value_for(path, "pipeline_workers")
            except (OSError, ValueError) as exc:
                failures.append(str(exc))
                continue
            if lanes != wanted:
                failures.append(f"{path.relative_to(ROOT)}: gpu_lanes={lanes}, expected {wanted} for {group} profile")
            if workers <= 0:
                failures.append(f"{path.relative_to(ROOT)}: pipeline_workers must be positive, got {workers}")
            print(f"{group}: {path.relative_to(ROOT)} pipeline_workers={workers} gpu_lanes={lanes}")
    # The producer's gate is deployed separately (systemd drop-in), not
    # represented by a RenderingGen profile file. It is intentionally not
    # conflated with gpu_lanes here; deployment wiring is verified by the
    # operator's live-contract audit.
    producer_slots = "separate deployment setting"
    if failures:
        print("GPU profile contract FAILED:", file=sys.stderr)
        print("\n".join(f" - {line}" for line in failures), file=sys.stderr)
        return 1
    print(f"GPU profile contract OK: native={EXPECTED['native']}, Docker={EXPECTED['docker']}, CI={EXPECTED['ci']}; producer admission is a separate deployment setting")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
