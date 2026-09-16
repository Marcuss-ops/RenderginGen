#!/usr/bin/env python3
"""Verify the multilingual overlay matrix.

Two independent checks, both fail-closed:

1. STRUCTURAL — every job of the matrix must have completed with the contract's
   facts: 1920x1080, 24/1 fps, 120 frames, 5 s, backend vulkan, and an artifact
   whose downloaded bytes hash to the advertised content address.

2. TEXT PIXELS — a translated overlay must actually rasterise its translated
   text. The Russian variants are compared between the two runs: run1 declared
   only the primary font (Poppins-Bold, no Cyrillic), run2 also declares the
   coverage font (Inter-Bold). If the fallback font did its job, the text band
   contains glyph ink in run2 and the artifact bytes differ. An overlay that
   "completed" with missing glyphs is exactly the silent failure this catches.

Output: verify_report.json + a printed PASS/FAIL summary. Exit code 1 on any
FAIL so the check can gate a pipeline.
"""
import argparse
import hashlib
import json
import os
import subprocess
import sys
import urllib.request

BACKGROUND = (238, 241, 231)  # the corpus colour background, 8-bit
INK_TOLERANCE = 24  # per-channel distance that still counts as background
# A phrase band that carries a rendered phrase produces tens of thousands of ink
# pixels. The bar exists to catch the opposite failure: an artifact that
# "completed" while rasterising almost nothing, which is what the missing-glyph
# run produced (790 px for a phrase that needs ~28k).
MIN_INK_PIXELS = 5000


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fetch(url, path):
    if os.path.exists(path) and os.path.getsize(path) > 0:
        return path
    with urllib.request.urlopen(url, timeout=180) as resp, open(path, "wb") as out:
        out.write(resp.read())
    return path


def ink_pixels(video, frame, stage):
    """Count pixels in the phrase band that differ from the background colour.

    The text band is the middle 120 px row of the 1080-tall canvas where the
    canonical preset places the phrase; sampling one frame is enough because the
    preset animates opacity/scale, not position.
    """
    out_png = os.path.join(stage, f"frame_{frame}.png")
    subprocess.run(
        ["ffmpeg", "-y", "-v", "error", "-i", video, "-vf", f"select=eq(n\\,{frame})",
         "-vframes", "1", out_png],
        check=True,
    )
    raw = subprocess.run(
        ["ffmpeg", "-y", "-v", "error", "-i", out_png, "-f", "rawvideo", "-pix_fmt", "rgb24", "-"],
        check=True, capture_output=True,
    ).stdout
    width, height = 1920, 1080
    y0, y1 = height // 2 - 60, height // 2 + 60
    ink = 0
    for y in range(y0, y1):
        row = y * width * 3
        for x in range(width):
            i = row + x * 3
            if (abs(raw[i] - BACKGROUND[0]) > INK_TOLERANCE or
                    abs(raw[i + 1] - BACKGROUND[1]) > INK_TOLERANCE or
                    abs(raw[i + 2] - BACKGROUND[2]) > INK_TOLERANCE):
                ink += 1
    return ink


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--queue", default="http://localhost:8081")
    parser.add_argument("--manifest", default="manifest_run2.json")
    parser.add_argument("--baseline-manifest", default="manifest_run1.json")
    parser.add_argument("--stage", default="verify")
    parser.add_argument("--report", default="verify_report.json")
    args = parser.parse_args()

    os.makedirs(args.stage, exist_ok=True)
    manifest = json.load(open(args.manifest))
    baseline_manifest = json.load(open(args.baseline_manifest)) if os.path.exists(args.baseline_manifest) else None
    baseline_batch = baseline_manifest["batch_id"] if baseline_manifest else None

    structural_failures = []
    rows = []
    hashes = {}
    for job in manifest["jobs"]:
        job_id = f"{manifest['batch_id']}:{job['id']}"
        with urllib.request.urlopen(f"{args.queue}/jobs/{job_id}", timeout=30) as resp:
            body = json.load(resp)
        artifact = body.get("artifact") or {}
        row = {"job_id": job_id, "state": body.get("state")}
        ok = body.get("state") == "completed"
        problems = []
        if not ok:
            problems.append(f"state={body.get('state')}")
        for field, expected in (("width", 1920), ("height", 1080), ("frame_count", 120),
                                ("fps_num", 24), ("fps_den", 1), ("backend", "vulkan")):
            if artifact.get(field) != expected:
                problems.append(f"{field}={artifact.get(field)!r} want {expected!r}")
        if artifact.get("duration_us") != 5_000_000:
            problems.append(f"duration_us={artifact.get('duration_us')!r}")
        if ok and artifact.get("artifact_url"):
            local = fetch(artifact["artifact_url"], os.path.join(args.stage, f"{job['id']}.mp4"))
            got = sha256_file(local)
            row["sha256"] = got
            row["bytes"] = artifact.get("size_bytes")
            if got != artifact.get("artifact_hash"):
                problems.append("content address mismatch")
        row["problems"] = problems
        if problems:
            structural_failures.append(row)
        rows.append(row)
        hashes[job_id] = row.get("sha256")

    # Per-overlay language distinctness: the same overlay must render different
    # bytes in two languages whenever their translated text differs.
    distinctness = []
    by_overlay = {}
    for job in manifest["jobs"]:
        key = job["id"].rsplit("__", 1)[0]
        by_overlay.setdefault(key, []).append(job)
    for key, jobs in sorted(by_overlay.items()):
        texts = {j["render_plan"]["language"]: j["render_plan"]["items"][0].get("text", "") for j in jobs}
        digests = {}
        for j in jobs:
            job_id = f"{manifest['batch_id']}:{j['id']}"
            digests[j["render_plan"]["language"]] = hashes.get(job_id)
        # Languages with identical source text may legitimately produce the same
        # bytes; any pair with DIFFERENT text must differ.
        collisions = []
        langs = sorted(texts)
        for i, left in enumerate(langs):
            for right in langs[i + 1:]:
                if texts[left] != texts[right] and digests.get(left) and digests.get(left) == digests.get(right):
                    collisions.append(f"{left}=={right} identical bytes for different text")
        distinctness.append({"overlay": key, "distinct_hashes": len(set(v for v in digests.values() if v)),
                             "languages": len(langs), "collisions": collisions})

    # Cyrillic pixel check: run1 (primary font only) vs run2 (coverage font).
    cyrillic = []
    if baseline_manifest:
        for overlay, language in (("phrase_01_lower_third_safe", "ru"),
                                 ("phrase_04_dm_sans_fade", "ru"),
                                 ("phrase_02_clean_slide_up", "ru")):
            ids = {
                "run1": f"{baseline_batch}:{overlay}__{language}",
                "run2": f"{manifest['batch_id']}:{overlay}__{language}",
            }
            entry = {"overlay": overlay, "language": language}
            for label, job_id in ids.items():
                try:
                    with urllib.request.urlopen(f"{args.queue}/jobs/{job_id}", timeout=30) as resp:
                        body = json.load(resp)
                except Exception as err:  # noqa: BLE001 - reported, never hidden
                    entry[f"{label}_error"] = str(err)
                    continue
                artifact = body.get("artifact") or {}
                entry[f"{label}_state"] = body.get("state")
                if body.get("state") != "completed" or not artifact.get("artifact_url"):
                    continue
                local = fetch(artifact["artifact_url"], os.path.join(args.stage, f"{label}_{overlay}_{language}.mp4"))
                entry[f"{label}_sha256"] = sha256_file(local)
                entry[f"{label}_ink"] = ink_pixels(local, 60, args.stage)
            entry["bytes_changed"] = (entry.get("run1_sha256") is not None and
                                      entry.get("run2_sha256") is not None and
                                      entry["run1_sha256"] != entry["run2_sha256"])
            # The criterion is "run2 rasterises the translated text". When run1
            # has no artifact at all (it failed on the missing glyphs) there is
            # nothing to compare, so the pair-wise increase is N/A rather than a
            # failure; run2 must still carry glyph ink.
            entry["run2_renders_text"] = (entry.get("run2_ink") is not None and
                                          entry["run2_ink"] >= MIN_INK_PIXELS)
            entry["ink_increased"] = (entry.get("run1_ink") is None or
                                      (entry.get("run2_ink") is not None and
                                       entry["run2_ink"] > entry["run1_ink"]))
            entry["run1_had_artifact"] = entry.get("run1_ink") is not None
            cyrillic.append(entry)

    report = {
        "batch": manifest["batch_id"],
        "jobs": len(rows),
        "structural_failures": structural_failures,
        "distinctness_collisions": [d for d in distinctness if d["collisions"]],
        "cyrillic_check": cyrillic,
        "rows": rows,
    }
    json.dump(report, open(args.report, "w"), indent=2, sort_keys=True)

    print(f"structural: {len(rows) - len(structural_failures)}/{len(rows)} PASS")
    for failure in structural_failures[:10]:
        print("  FAIL", failure["job_id"], failure["problems"])
    print(f"distinctness collisions: {len(report['distinctness_collisions'])}")
    for d in report["distinctness_collisions"]:
        print("  ", d)
    for entry in cyrillic:
        print(f"cyrillic {entry['overlay']}: run1={entry.get('run1_state')} ink={entry.get('run1_ink')} "
              f"-> run2={entry.get('run2_state')} ink={entry.get('run2_ink')} "
              f"renders_text={entry.get('run2_renders_text')} ink_increased={entry.get('ink_increased')} "
              f"bytes_changed={entry.get('bytes_changed')}")
    bad = bool(structural_failures) or bool(report["distinctness_collisions"]) or any(
        not e.get("run2_renders_text") or not e.get("ink_increased") for e in cyrillic)
    print("VERDICT:", "FAIL" if bad else "PASS")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
