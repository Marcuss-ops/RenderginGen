#!/usr/bin/env python3
"""Scale the frozen T0-01 text-parity plans to 1920x1080 demo plans.

The fixtures in Chronon3d/tests/text/fixtures/pixel_parity are frozen by SHA in
their manifest and must not be edited. This script copies them, scales the
pixel-space fields by 1.5 (720p -> 1080p), keeps the authored animation frames,
and extends the static cases to a 3 second clip. The output is written next to
this script in plans/ and a plans.json index is produced for the renderer.
"""
from __future__ import annotations

import json
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
SRC = ROOT / "Chronon3d/tests/text/fixtures/pixel_parity"
PLANS = HERE / "plans"
SCALE = 1.5

# (slug, clip length in frames, comparison software render)
CASES = [
    ("latin_basic", 90, True),
    ("exact_3d", 120, False),
    ("effects", 61, False),
    ("multi_shadow", 90, False),
    ("rtl_caret", 90, False),
    ("transitions", 61, False),
    ("fractional", 90, True),
    ("multipage", 90, False),
]


def scaled_vec(value: list[float]) -> list[float]:
    return [round(float(component) * SCALE, 4) for component in value]


def scale_plan(doc: dict, duration: int) -> dict:
    doc["canvas"]["width"] = 1920
    doc["canvas"]["height"] = 1080
    doc["canvas"]["duration_frames"] = duration
    doc["job_id"] = f"{doc.get('job_id', 'text-parity')}-1080p"

    for layer in doc.get("layers", []):
        layer["start_frame"] = 0
        layer["duration_frames"] = duration
        if "position" in layer:
            layer["position"] = scaled_vec(layer["position"])
        if "size" in layer:
            layer["size"] = scaled_vec(layer["size"])

        style = layer.get("style", {})
        if "font_size" in style:
            style["font_size"] = round(float(style["font_size"]) * SCALE, 4)
        stroke = style.get("stroke")
        if stroke and "width" in stroke:
            stroke["width"] = round(float(stroke["width"]) * SCALE, 4)
        for shadow in style.get("shadows", []):
            if "blur" in shadow:
                shadow["blur"] = round(float(shadow["blur"]) * SCALE, 4)
            if "offset" in shadow:
                shadow["offset"] = scaled_vec(shadow["offset"])
        background = style.get("background")
        if background:
            if "radius" in background:
                background["radius"] = round(
                    float(background["radius"]) * SCALE, 4)
            if "padding" in background:
                background["padding"] = scaled_vec(background["padding"])
        caret = style.get("text_caret")
        if caret and caret.get("width"):
            caret["width"] = round(float(caret["width"]) * SCALE, 4)

        for effect in layer.get("effects", []):
            if "radius" in effect:
                effect["radius"] = round(float(effect["radius"]) * SCALE, 4)
    return doc


def main() -> int:
    PLANS.mkdir(parents=True, exist_ok=True)
    index = []
    for number, (slug, frames, software) in enumerate(CASES, start=1):
        source = SRC / f"{slug}.plan.json"
        document = json.loads(source.read_text())
        duration_s = frames / 30.0
        stem = f"{number:02d}_{slug}_1920x1080_{duration_s:g}s"
        document["output"]["path"] = f"videos/{stem}.mp4"
        document = scale_plan(document, frames)
        plan_path = PLANS / f"{number:02d}_{slug}.plan.json"
        plan_path.write_text(json.dumps(document, indent=2) + "\n")
        index.append({
            "number": number,
            "slug": slug,
            "plan": str(plan_path.relative_to(HERE)),
            "frames": frames,
            "duration_s": duration_s,
            "width": 1920,
            "height": 1080,
            "fps_num": 30,
            "fps_den": 1,
            "software_comparison": software,
            "video_stem": stem,
        })
        print(f"plan {plan_path.relative_to(HERE)} "
              f"({frames} frames, {duration_s:g}s)")

    (HERE / "plans.json").write_text(json.dumps(
        {"cases": index}, indent=2) + "\n")
    print(f"wrote {len(index)} plans")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
