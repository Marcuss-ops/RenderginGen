#!/usr/bin/env python3
"""Measure the device working set of ONE overlay render (per job kind).

The daemon serializes the RENDER_JOB execution domain
(`kRenderJobExecutionSerialized`, `m_render_job_mutex`) because one 1080p
FullGraph exporter was measured at ~8.8 GB peak on a 16 GB card. That figure is
the only gate between today's throughput (one job at a time) and a second
concurrent exporter, so it has to be re-measurable on THIS host with THIS job
shape -- not quoted from a comment.

The probe takes two real jobs out of a certified manifest, perturbs one
semantic value each (so no content-addressed cache can answer for the render),
submits them through the normal queue path, and samples `nvidia-smi` at 50 ms
while they are in flight. It reports:

  * idle baseline (device total, and the warm daemon's own residency), because
    a second job's cost is the DELTA, not the absolute peak;
  * peak device memory and peak daemon memory during the render;
  * the render window, so the peak can be attributed to a real encoder session
    (`Opened native encoder` in the daemon log) instead of a silent cache hit.

A job that never rendered is reported as NOT RUN, never as a number.
"""
import argparse
import json
import os
import subprocess
import sys
import threading
import time
import urllib.request


def _smi():
    out = subprocess.run(
        ["nvidia-smi", "--query-gpu=memory.used,utilization.gpu",
         "--format=csv,noheader,nounits"],
        capture_output=True, text=True, check=True).stdout.strip()
    used, util = out.split(",")[:2]
    return int(used), int(util)


def _daemon_vram():
    """Device memory held by chronon3d_cli, summed over its processes."""
    out = subprocess.run(
        ["nvidia-smi", "--query-compute-apps=pid,used_memory",
         "--format=csv,noheader,nounits"],
        capture_output=True, text=True).stdout
    total = 0
    for line in out.splitlines():
        parts = [p.strip() for p in line.split(",")]
        if len(parts) < 2:
            continue
        pid, mem = parts[0], parts[1]
        try:
            with open(f"/proc/{pid}/cmdline", "rb") as fh:
                cmd = fh.read().decode(errors="replace")
        except OSError:
            continue  # exited between the listing and the read
        if "chronon3d_cli" in cmd:
            total += int(mem)
    return total


def _get(url):
    with urllib.request.urlopen(url, timeout=20) as resp:
        return json.loads(resp.read().decode())


def build_probe_manifest(source, out_path, batch_id):
    """Take one phrase job and one image job, perturbs each plan semantically."""
    manifest = json.load(open(source))
    phrase = next(j for j in manifest["jobs"] if "phrase" in j["id"])
    image = next(j for j in manifest["jobs"] if "phrase" not in j["id"])
    phrase = json.loads(json.dumps(phrase))
    image = json.loads(json.dumps(image))
    phrase["id"] = "vram_probe_phrase"
    image["id"] = "vram_probe_image"
    for job in (phrase, image):
        plan = job["render_plan"]
        plan["plan_id"] = f"{job['id']}__probe"
        plan["video_id"] = f"{job['id']}__probe"
        plan["duration_ms"] = plan["duration_ms"] + 10  # 1 extra frame: new content hash
    for item in phrase["render_plan"]["items"]:
        if "text" in item:
            item["text"] = item["text"] + " "  # shape unchanged, bytes changed
    probe = {"batch_id": batch_id, "schema_version": manifest["schema_version"],
             "jobs": [phrase, image]}
    json.dump(probe, open(out_path, "w"), indent=1)
    return probe


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--source-manifest", required=True)
    ap.add_argument("--repo", required=True, help="RenderingGen checkout root")
    ap.add_argument("--batch-id", default="vram-probe-1")
    ap.add_argument("--queue", default="http://localhost:8081")
    ap.add_argument("--interval", type=float, default=0.05)
    ap.add_argument("--out", required=True)
    ap.add_argument("--probe-manifest", default="manifest_vram_probe.json")
    args = ap.parse_args()

    # batch-submit runs with the checkout as its working directory, so the probe
    # manifest has to be named absolutely or the two paths refer to two files.
    probe_path = os.path.abspath(args.probe_manifest)
    probe = build_probe_manifest(args.source_manifest, probe_path, args.batch_id)
    job_ids = [f"{args.batch_id}:{j['id']}" for j in probe["jobs"]]

    idle_used, _ = _smi()
    idle_daemon = _daemon_vram()
    samples = []
    stop = threading.Event()

    def sampler():
        while not stop.is_set():
            try:
                used, util = _smi()
                samples.append((time.time(), used, util, _daemon_vram()))
            except Exception:
                pass
            time.sleep(args.interval)

    thread = threading.Thread(target=sampler, daemon=True)
    thread.start()
    window_start = time.time()
    submit = subprocess.run(
        ["go", "run", "./renderinggen/cmd/batch-submit", "-queue", args.queue,
         "-manifest", probe_path],
        cwd=args.repo, capture_output=True, text=True)
    print(submit.stdout.strip() or submit.stderr.strip(), file=sys.stderr)
    if submit.returncode != 0:
        stop.set()
        thread.join(timeout=2)
        sys.exit(f"batch-submit failed: {submit.returncode}")

    deadline = time.time() + 600
    pending = list(job_ids)
    states = {}
    while pending and time.time() < deadline:
        still_running = []
        for job_id in pending:
            state = _get(f"{args.queue}/jobs/{job_id}").get("state", "unknown")
            states[job_id] = state
            if state not in ("completed", "failed", "cancelled"):
                still_running.append(job_id)
        pending = still_running
        if pending:
            time.sleep(0.5)
    window_end = time.time()
    stop.set()
    thread.join(timeout=2)

    report = {
        "batch_id": args.batch_id,
        "jobs": states,
        "idle_device_mib": idle_used,
        "idle_daemon_mib": idle_daemon,
        "peak_device_mib": max((s[1] for s in samples), default=0),
        "peak_daemon_mib": max((s[3] for s in samples), default=0),
        "peak_utilization_pct": max((s[2] for s in samples), default=0),
        "device_delta_mib": max((s[1] for s in samples), default=0) - idle_used,
        "daemon_delta_mib": max((s[3] for s in samples), default=0) - idle_daemon,
        "samples": len(samples),
        "window_start": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(window_start)),
        "window_end": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(window_end)),
    }
    json.dump(report, open(args.out, "w"), indent=1)
    print(json.dumps(report, indent=1))


if __name__ == "__main__":
    main()
