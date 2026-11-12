#!/usr/bin/env python3
"""Verify the canonical delivery manifest.

A queue state of SUCCEEDED is not a delivery certificate. This validator makes
the audit's minimum manifest explicit and fail-closed. Usage:

    python3 tools/verify_delivery_manifest.py manifest.json

Exit 0 only when the manifest is structurally complete and internally
coherent. A SUCCEEDED manifest with a failed test, a missing frame check, a
zero frame count or a malformed SHA-256 is rejected.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

SHA256 = re.compile(r"^[a-f0-9]{64}$")
SUCCESS = "SUCCEEDED"


def _positive_int(value: object) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value > 0


def _nonempty(value: object) -> bool:
    return isinstance(value, str) and bool(value.strip())


def validate(manifest: dict) -> list[str]:
    errors: list[str] = []
    if not isinstance(manifest, dict):
        return ["manifest must be a JSON object"]

    for field in (
        "schema_version",
        "job_id",
        "logical_job_id",
        "attempt_id",
        "status",
        "artifact",
        "media",
        "frame_checks",
        "code",
        "tests",
        "approval",
    ):
        if field not in manifest:
            errors.append(f"missing field: {field}")
    if errors:
        return errors

    for field in ("schema_version", "job_id", "logical_job_id", "attempt_id"):
        if not _nonempty(manifest[field]):
            errors.append(f"{field} must be a non-empty string")

    artifact = manifest["artifact"]
    if not isinstance(artifact, dict):
        errors.append("artifact must be an object")
    else:
        if not SHA256.match(str(artifact.get("sha256", ""))):
            errors.append("artifact.sha256 must be 64 lowercase hex characters")
        if not _nonempty(artifact.get("uri")):
            errors.append("artifact.uri must be a non-empty string")
        if not _positive_int(artifact.get("size_bytes")):
            errors.append("artifact.size_bytes must be positive")

    media = manifest["media"]
    if not isinstance(media, dict):
        errors.append("media must be an object")
    else:
        for field in ("duration_us", "width", "height", "fps_num", "fps_den", "frame_count"):
            if not _positive_int(media.get(field)):
                errors.append(f"media.{field} must be a positive integer")
        if not _nonempty(media.get("codec")):
            errors.append("media.codec must be a non-empty string")

    frame_checks = manifest["frame_checks"]
    if not isinstance(frame_checks, dict):
        errors.append("frame_checks must be an object")
    else:
        for position in ("first", "middle", "last"):
            check = frame_checks.get(position)
            if not isinstance(check, dict):
                errors.append(f"frame_checks.{position} must be an object")
                continue
            if not isinstance(check.get("frame_index"), int) or check["frame_index"] < 0:
                errors.append(f"frame_checks.{position}.frame_index must be >= 0")
            if not SHA256.match(str(check.get("sha256", ""))):
                errors.append(f"frame_checks.{position}.sha256 must be 64 lowercase hex characters")

    code = manifest["code"]
    if not isinstance(code, dict):
        errors.append("code must be an object")
    else:
        for field in ("pipelinegen_sha", "renderinggen_sha"):
            if not _nonempty(code.get(field)):
                errors.append(f"code.{field} must be a non-empty string")

    tests = manifest["tests"]
    if not isinstance(tests, list) or not tests:
        errors.append("tests must be a non-empty list")
    else:
        for index, test in enumerate(tests):
            if not isinstance(test, dict) or not _nonempty(test.get("name")):
                errors.append(f"tests[{index}].name must be a non-empty string")
            if not isinstance(test, dict) or test.get("status") not in ("PASS", "FAIL", "SKIP"):
                errors.append(f"tests[{index}].status must be PASS, FAIL or SKIP")

    approval = manifest["approval"]
    if not isinstance(approval, dict):
        errors.append("approval must be an object")
    else:
        if approval.get("approved") is not True:
            errors.append("approval.approved must be true")
        if not _nonempty(approval.get("approved_by")):
            errors.append("approval.approved_by must be a non-empty string")

    if manifest["status"] == SUCCESS:
        if isinstance(tests, list) and any(
            isinstance(test, dict) and test.get("status") == "FAIL" for test in tests
        ):
            errors.append("SUCCEEDED manifest cannot contain a FAIL test")
        for field in ("artifact", "media", "frame_checks", "tests", "approval"):
            if field in manifest and any(error.startswith(field) for error in errors):
                errors.append(f"SUCCEEDED requires a coherent {field}")
    elif manifest["status"] not in ("FAILED", "CANCELLED", "REJECTED"):
        errors.append("status must be SUCCEEDED, FAILED, CANCELLED or REJECTED")

    return errors


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(f"usage: {argv[0]} MANIFEST.json", file=sys.stderr)
        return 2
    path = Path(argv[1])
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        print(f"{path}: {error}", file=sys.stderr)
        return 2
    errors = validate(manifest)
    if errors:
        for error in errors:
            print(f"{path}: {error}", file=sys.stderr)
        return 1
    print(f"{path}: delivery manifest valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
