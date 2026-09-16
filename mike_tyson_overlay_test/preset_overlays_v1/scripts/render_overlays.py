#!/usr/bin/env python3
"""Build and render the fixed Mike Tyson overlay preset corpus.

This script consumes the existing 2,000-word request without regenerating its
narration or audio. It renders ten editorial phrase overlays and five entity
image overlays through the live RenderingGen queue. Motion is evaluated by the
renderer from each item's official preset/motion id at render time.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import statistics
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path


SCRIPT = Path(__file__).resolve()
WORK_DIR = SCRIPT.parents[1]
REPO_ROOT = SCRIPT.parents[3]
REQUEST = REPO_ROOT / "mike_tyson_overlay_test" / "mike_tyson_2000w_5entities_10phrases_en_sourcebacked_e2b_request.json"
OUT = WORK_DIR
WIDTH, HEIGHT, FPS_NUM, FPS_DEN, DURATION_MS = 1920, 1080, 24, 1, 5000
BACKGROUND = [0.9333333333333333, 0.9450980392156862, 0.9058823529411765, 1.0]

PHRASE_MOTIONS = [
    "kinetic_split_word",
    "masked_upward_reveal",
    "depth_of_field_rack_focus",
    "staggered_char_float",
    "dynamic_island_expansion",
    "soft_edge_spotlight_dissolve",
    "velocity_inertia_snap",
    "editorial_push_in",
    "liquid_glass_ripple",
    "chromatic_aberration_pop",
]

ENTITIES = [
    ("person:mike-tyson", "Mike Tyson", "mike_tyson.png", "image_focus_in", "image/png", "AI editorial illustration, generated for this project"),
    ("person:cus-damato", "Cus D’Amato", "cus_damato.png", "image_fade_in", "image/png", "AI editorial illustration, generated for this project"),
    ("person:muhammad-ali", "Muhammad Ali", "muhammad_ali.jpg", "image_scale_in", "image/jpeg", "Wikimedia Commons, public-domain status stated on source page"),
    ("person:sugar-ray-robinson", "Sugar Ray Robinson", "sugar_ray_robinson.jpg", "image_slide_left", "image/jpeg", "Library of Congress / Wikimedia Commons; no known copyright restriction stated on source page"),
    ("person:joe-frazier", "Joe Frazier", "joe_frazier.jpg", "image_slide_right", "image/jpeg", "Associated Press photo via Wikimedia Commons; U.S. public-domain/no-notice status stated on source page"),
]

IMAGE_SOURCES = {
    "muhammad_ali.jpg": "https://commons.wikimedia.org/wiki/File:Muhammad_Ali_Smiling_1962_Portrait.jpg",
    "sugar_ray_robinson.jpg": "https://commons.wikimedia.org/wiki/File:Sugar_Ray_Robinson_1965_(cropped).jpg",
    "joe_frazier.jpg": "https://commons.wikimedia.org/wiki/File:Joe_Frazier_1971_Press_Photo.jpg",
}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def api_json(url: str, body: dict | None = None, timeout: float = 30) -> dict:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as response:
        raw = response.read()
    return json.loads(raw.decode("utf-8")) if raw else {}


def make_manifest(asset_base_url: str, batch_id: str) -> dict:
    request = json.loads(REQUEST.read_text(encoding="utf-8"))
    item = request["items"][0]
    segments = item["script_params"]["segments"]
    extraction = item["media_plan"]["extraction"]
    phrases = extraction["important_phrases"]
    if len(phrases) != 10 or len(segments) != 10:
        raise ValueError("the fixed request must contain ten segments and ten test phrases")
    for phrase, segment in zip(phrases, segments):
        if phrase not in segment["source_text"]:
            raise ValueError(f"phrase is not in its fixed source segment: {phrase}")

    asset_base_url = asset_base_url.rstrip("/")
    font_specs = [
        ("testdata/golden/assets/fonts/Poppins-Bold.ttf", "assets/fonts/Poppins-Bold.ttf"),
        ("testdata/golden/assets/fonts/Inter-Bold.ttf", "assets/fonts/Inter-Bold.ttf"),
    ]
    fonts = []
    for local, logical in font_specs:
        path = REPO_ROOT / local
        fonts.append({
            "hash": sha256(path),
            "logical_path": logical,
            "source_url": f"{asset_base_url}/{local}",
        })

    jobs = []
    for index, (phrase, segment, motion) in enumerate(zip(phrases, segments, PHRASE_MOTIONS), 1):
        job_id = f"{batch_id}-phrase-{index:02d}"
        plan = {
            "schema_version": "renderinggen.overlay-plan.v1",
            "plan_id": job_id,
            "video_id": job_id,
            "project_id": "mike-tyson-overlay-presets-v1",
            "language": "en",
            "width": WIDTH,
            "height": HEIGHT,
            "fps_num": FPS_NUM,
            "fps_den": FPS_DEN,
            "duration_ms": DURATION_MS,
            "background": {"kind": "color", "color": BACKGROUND},
            "items": [{
                "id": "important-phrase",
                "scene_id": segment["id"],
                "kind": "important_phrase",
                "template_id": "IMPORTANT_PHRASE",
                "preset_id": "apple_v2",
                "motion_id": motion,
                "text": phrase,
                "start_ms": 0,
                "end_ms": DURATION_MS,
            }],
        }
        jobs.append({"id": job_id, "family": "phrase", "text": phrase, "motion_id": motion, "render_plan": plan, "assets": fonts})

    for index, entity in enumerate(ENTITIES, 1):
        entity_id, name, filename, preset, media_type, attribution = entity
        local_path = OUT / "assets" / "entities" / filename
        logical_path = f"assets/entities/{filename}"
        asset_url = f"{asset_base_url}/mike_tyson_overlay_test/preset_overlays_v1/assets/entities/{urllib.parse.quote(filename)}"
        asset_hash = sha256(local_path)
        job_id = f"{batch_id}-image-{index:02d}"
        asset_ref = {"asset_id": entity_id, "url": asset_url, "sha256": asset_hash, "media_type": media_type}
        plan = {
            "schema_version": "renderinggen.overlay-plan.v1",
            "plan_id": job_id,
            "video_id": job_id,
            "project_id": "mike-tyson-overlay-presets-v1",
            "language": "en",
            "width": WIDTH,
            "height": HEIGHT,
            "fps_num": FPS_NUM,
            "fps_den": FPS_DEN,
            "duration_ms": DURATION_MS,
            "background": {"kind": "color", "color": BACKGROUND},
            "items": [{
                "id": "entity-image",
                "entity_id": entity_id,
                "kind": "entity_image",
                "template_id": "IMAGE_OVERLAY",
                "preset_id": preset,
                "text": name,
                "start_ms": 0,
                "end_ms": DURATION_MS,
                "duration_ms": DURATION_MS,
                "asset_refs": [asset_ref],
            }],
        }
        assets = [*fonts, {"hash": asset_hash, "logical_path": logical_path, "source_url": asset_url}]
        jobs.append({
            "id": job_id, "family": "image", "entity_id": entity_id, "entity_name": name,
            "preset_id": preset, "asset": filename, "asset_sha256": asset_hash,
            "asset_attribution": attribution, "asset_source": IMAGE_SOURCES.get(filename, "Generated for this project"),
            "render_plan": plan, "assets": assets,
        })
    return {
        "batch_id": batch_id,
        "source_request": str(REQUEST.relative_to(REPO_ROOT)),
        "duration_ms": DURATION_MS,
        "fps_num": FPS_NUM,
        "fps_den": FPS_DEN,
        "background": {"kind": "color", "color": BACKGROUND, "hex": "#EEF1E7"},
        "text_style": {"preset_id": "apple_v2", "fill": "#FFFFFF", "stroke": "#111827", "shadow": "black 72%"},
        "jobs": jobs,
    }


def parse_time(value: str | None) -> datetime | None:
    if not value:
        return None
    match = re.match(
        r"^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})?$",
        value,
    )
    if not match:
        return None
    whole, fraction, timezone_text = match.groups()
    value = whole
    if fraction:
        value += "." + fraction[:6].ljust(6, "0")
    if timezone_text == "Z":
        value += "+00:00"
    elif timezone_text:
        value += timezone_text
    try:
        return datetime.fromisoformat(value)
    except ValueError:
        return None


def download(url: str, target: Path, expected_hash: str | None = None) -> None:
    target.parent.mkdir(parents=True, exist_ok=True)
    with urllib.request.urlopen(url, timeout=180) as response, target.open("wb") as handle:
        while True:
            chunk = response.read(1 << 20)
            if not chunk:
                break
            handle.write(chunk)
    actual = sha256(target)
    if expected_hash and actual != expected_hash:
        target.unlink(missing_ok=True)
        raise ValueError(f"download SHA-256 mismatch for {target.name}: {actual} != {expected_hash}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--queue", default="http://127.0.0.1:8081")
    parser.add_argument("--asset-base-url", default="http://127.0.0.1:8099")
    parser.add_argument("--batch-id", default=f"tyson-overlays-{datetime.now(timezone.utc):%Y%m%d-%H%M%S}")
    parser.add_argument("--timeout", type=float, default=1800)
    parser.add_argument("--build-only", action="store_true")
    args = parser.parse_args()

    manifest = make_manifest(args.asset_base_url, args.batch_id)
    docs = OUT / "docs"
    docs.mkdir(parents=True, exist_ok=True)
    (docs / "render_manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    if args.build_only:
        print(f"built {len(manifest['jobs'])} jobs: {docs / 'render_manifest.json'}")
        return 0

    queue = args.queue.rstrip("/")
    api_json(queue + "/health")
    for job in manifest["jobs"]:
        payload = {"id": job["id"], "schema": "renderinggen.job", "version": 1,
                   "render_plan": job["render_plan"], "assets": job["assets"]}
        try:
            api_json(queue + "/jobs", payload)
        except urllib.error.HTTPError as error:
            if error.code != 409:
                raise
        print(f"submitted {job['id']}", flush=True)

    jobs_by_id = {job["id"]: job for job in manifest["jobs"]}
    latest: dict[str, dict] = {}
    deadline = time.monotonic() + args.timeout
    while time.monotonic() < deadline:
        terminal = 0
        for job_id in jobs_by_id:
            try:
                body = api_json(f"{queue}/jobs/{urllib.parse.quote(job_id, safe='')}")
            except urllib.error.HTTPError as error:
                if error.code == 404:
                    continue
                raise
            latest[job_id] = body
            terminal += body.get("state") in {"completed", "failed", "cancelled"}
        print(f"rendered/terminal {terminal}/{len(jobs_by_id)}", flush=True)
        if terminal == len(jobs_by_id):
            break
        time.sleep(2)

    (docs / "jobs_snapshot.json").write_text(json.dumps(latest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    timing = {
        "schema": "mike-tyson-overlay-timing.v1",
        "batch_id": args.batch_id,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "canvas": {"width": WIDTH, "height": HEIGHT, "fps_num": FPS_NUM, "fps_den": FPS_DEN,
                   "duration_ms": DURATION_MS, "frames": round(DURATION_MS * FPS_NUM / (1000 * FPS_DEN))},
        "background": manifest["background"],
        "text_style": manifest["text_style"],
        "jobs": [],
    }
    terminal_states = {"completed", "failed", "cancelled"}
    for job_id, job in jobs_by_id.items():
        body = latest.get(job_id, {})
        artifact = body.get("artifact") or {}
        queued, began, ended = (parse_time(body.get(k)) for k in ("queued_at", "started_at", "completed_at"))
        row = {
            "job_id": job_id,
            "family": job["family"],
            "entity_id": job.get("entity_id"),
            "entity_name": job.get("entity_name"),
            "text": job.get("text"),
            "preset_id": job.get("preset_id") or job["render_plan"]["items"][0]["preset_id"],
            "motion_id": job.get("motion_id") or job["render_plan"]["items"][0].get("motion_id"),
            "asset": job.get("asset"),
            "asset_source": job.get("asset_source"),
            "asset_attribution": job.get("asset_attribution"),
            "state": body.get("state", "not_found"),
            "queued_at": body.get("queued_at"),
            "started_at": body.get("started_at"),
            "completed_at": body.get("completed_at"),
            "queue_wait_ms": int((began - queued).total_seconds() * 1000) if began and queued else None,
            "render_wall_ms": int((ended - began).total_seconds() * 1000) if ended and began else None,
            "artifact_sha256": artifact.get("artifact_hash") or artifact.get("sha256"),
            "artifact_url": artifact.get("artifact_url"),
            "timing_sha256": artifact.get("chronon_timing_sha256"),
            "timing_url": artifact.get("chronon_timing_url"),
        }
        if body.get("state") == "completed" and row["artifact_url"]:
            family_dir = "phrases" if job["family"] == "phrase" else "images"
            video_path = OUT / "renders" / family_dir / f"{job_id}.mp4"
            download(row["artifact_url"], video_path, row["artifact_sha256"])
            row["local_video"] = str(video_path.relative_to(OUT))
            if row["timing_url"]:
                timing_path = docs / "timing" / f"{job_id}.timing.json"
                download(row["timing_url"], timing_path, row["timing_sha256"])
                row["local_timing"] = str(timing_path.relative_to(OUT))
                raw_timing = json.loads(timing_path.read_text(encoding="utf-8"))
                row["chronon_wall_time_ms"] = raw_timing.get("wall_time_ms")
                row["chronon_render_ms"] = raw_timing.get("render_ms")
                row["chronon_frames_total"] = raw_timing.get("frames_total")
                row["chronon_target_fps"] = (raw_timing.get("summary") or {}).get("target_fps")
        timing["jobs"].append(row)

    completed_rows = [job for job in timing["jobs"] if job["state"] == "completed"]

    def stats(values: list[float]) -> dict | None:
        values = sorted(values)
        if not values:
            return None
        return {"count": len(values), "min_ms": round(values[0], 3),
                "p50_ms": round(statistics.median(values), 3), "max_ms": round(values[-1], 3),
                "mean_ms": round(statistics.mean(values), 3)}

    queued_times = [parse_time(job.get("queued_at")) for job in timing["jobs"]]
    completed_times = [parse_time(job.get("completed_at")) for job in completed_rows]
    queued_times = [value for value in queued_times if value]
    completed_times = [value for value in completed_times if value]
    timing["summary"] = {
        "jobs_total": len(jobs_by_id),
        "completed": len(completed_rows),
        "failed": sum(job["state"] == "failed" for job in timing["jobs"]),
        "not_terminal": sum(job["state"] not in terminal_states for job in timing["jobs"]),
        "phrase_overlays": sum(job["family"] == "phrase" for job in completed_rows),
        "image_overlays": sum(job["family"] == "image" for job in completed_rows),
        "timing_sidecars": sum(bool(job.get("local_timing")) for job in timing["jobs"]),
        "queue_wall_ms": int((max(completed_times) - min(queued_times)).total_seconds() * 1000)
        if queued_times and completed_times else None,
        "queue_render_wall_ms": stats([job["render_wall_ms"] for job in completed_rows if job.get("render_wall_ms") is not None]),
        "chronon_wall_time_ms": stats([job["chronon_wall_time_ms"] for job in completed_rows if job.get("chronon_wall_time_ms") is not None]),
    }
    (docs / "timing.json").write_text(json.dumps(timing, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(timing["summary"], indent=2), flush=True)
    return 0 if len(completed_rows) == len(jobs_by_id) else 1


if __name__ == "__main__":
    sys.exit(main())
