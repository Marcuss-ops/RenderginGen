#!/usr/bin/env python3
"""Render the 1080p text-parity demo bundle.

Every case is rendered through the strict native Vulkan/MTSDF route. The two
cases that exercise subpixel placement (latin_basic and fractional) are also
rendered through the software reference path, which is where the integer
scratch-surface origin fix lives. Stills are extracted early/mid/late from each
video and a content-addressed MANIFEST.json is written for the Drive upload.
"""
from __future__ import annotations

import hashlib
import json
import subprocess
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
_CLI_CANDIDATES = [
    ROOT / "Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli",
    ROOT / "Chronon3d/build/chronon/linux-video-release.root-bin/apps/chronon3d_cli/chronon3d_cli",
]
CLI = max((path for path in _CLI_CANDIDATES if path.is_file()),
          key=lambda path: path.stat().st_mtime)
ASSETS = (ROOT / "Chronon3d").resolve()
VIDEOS = HERE / "videos"
STILLS = HERE / "stills"
LOGS = HERE / "logs"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def render(plan: Path, video: Path, backend: str, log: Path) -> float:
    command = [
        str(CLI), "render",
        "--plan", str(plan),
        "--assets-root", str(ASSETS),
        "--backend", backend,
        "--profile", "production",
        "--tone-map", "aces",
        "--exposure", "1.0",
        "-o", str(video),
        "-v", "info",
    ]
    if backend == "vulkan":
        command += ["--gpu-hot-path-mode", "require_gpu_native"]
    started = time.time()
    with log.open("w") as handle:
        handle.write("$ " + " ".join(command) + "\n")
        handle.flush()
        result = subprocess.run(command, stdout=handle,
                                stderr=subprocess.STDOUT)
    elapsed = time.time() - started
    if result.returncode != 0:
        raise RuntimeError(
            f"{backend} render failed ({result.returncode}); see {log}")
    return elapsed


def extract_still(video: Path, frame: int, out: Path) -> None:
    result = subprocess.run([
        "ffmpeg", "-y", "-loglevel", "error", "-i", str(video),
        "-vf", f"select=eq(n\\,{frame})", "-frames:v", "1", str(out),
    ], capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"ffmpeg still failed: {result.stderr.strip()}")


def main() -> int:
    if not CLI.is_file():
        raise SystemExit(f"missing CLI: {CLI}")
    index = json.loads((HERE / "plans.json").read_text())["cases"]
    for directory in (VIDEOS, STILLS, LOGS):
        directory.mkdir(parents=True, exist_ok=True)

    manifest: dict = {
        "schema": "chronon.text-parity-subpixel.drive-delivery.v1",
        "date": "2026-11-10",
        "notes": (
            "T0-01 text pixel-parity corpus at 1920x1080. Each case is one "
            "point of the frozen corpus; latin_basic and fractional also have "
            "a software-reference render for the integer scratch-surface "
            "origin fix. Clips are 2-4 seconds."
        ),
        "canvas": {"width": 1920, "height": 1080, "fps": "30/1"},
        "cli": str(CLI.relative_to(ROOT)),
        "cli_sha256": sha256_file(CLI),
        "cases": [],
    }

    for case in index:
        plan = HERE / case["plan"]
        entries = [("vulkan", "")]
        if case["software_comparison"]:
            entries.append(("software", "_software"))
        for backend, suffix in entries:
            stem = case["video_stem"] + suffix
            video = VIDEOS / f"{stem}.mp4"
            log = LOGS / f"{stem}.log"
            print(f"render {stem} [{backend}] ...", flush=True)
            seconds = render(plan, video, backend, log)
            stills = []
            for label, frame in (
                    ("early", 0),
                    ("mid", case["frames"] // 2),
                    ("late", case["frames"] - 1)):
                still = STILLS / f"{stem}_{label}.png"
                extract_still(video, frame, still)
                stills.append(str(still.relative_to(HERE)))
            manifest["cases"].append({
                "id": case["slug"],
                "number": case["number"],
                "backend": backend,
                "plan": case["plan"],
                "plan_sha256": sha256_file(plan),
                "video": str(video.relative_to(HERE)),
                "video_sha256": sha256_file(video),
                "bytes": video.stat().st_size,
                "width": case["width"],
                "height": case["height"],
                "fps": "30/1",
                "frames": case["frames"],
                "duration_s": case["duration_s"],
                "render_seconds": round(seconds, 3),
                "log": str(log.relative_to(HERE)),
                "stills": stills,
            })
            print(f"  -> {video.name} ({video.stat().st_size} bytes, "
                  f"{seconds:.1f}s)")

    (HERE / "MANIFEST.json").write_text(
        json.dumps(manifest, indent=2) + "\n")
    print("RENDER_BUNDLE_PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
