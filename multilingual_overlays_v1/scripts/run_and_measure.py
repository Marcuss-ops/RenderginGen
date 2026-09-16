#!/usr/bin/env python3
"""Poll the 100-job multilingual overlay matrix and record the measured speed.

Reads the batch manifest to know the job ids, polls the central queue's
GET /jobs/{id} until every job is terminal, then writes:

  jobs_snapshot.json  the final queue body of every job (the evidence of record)
  summary.json        derived timings: per job, per language, and the run total

Nothing is estimated: every number comes from a queue timestamp or from the
artifact the worker certified. A job that never reaches a terminal state is
reported as NOT RUN, never as a success.
"""
import argparse
import json
import re
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime

TERMINAL = {"completed", "failed", "cancelled"}


TIMESTAMP = re.compile(
    r"^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})?$"
)


def parse_ts(value):
    """Parse a queue timestamp, tolerating nanosecond precision.

    Go's RFC3339Nano emits up to 9 fractional digits, which datetime.fromisoformat
    rejects; the fraction is therefore truncated to microseconds instead of
    silently returning None (a None timestamp must never look like a measurement
    of zero).
    """
    if not value:
        return None
    match = TIMESTAMP.match(value)
    if not match:
        return None
    head, frac, tz = match.groups()
    text = head
    if frac:
        text += f".{frac[:6].ljust(6, '0')}"
    if tz and tz != "Z":
        text += tz
    try:
        return datetime.fromisoformat(text)
    except ValueError:
        return None


def get(url):
    with urllib.request.urlopen(url, timeout=20) as resp:
        return json.loads(resp.read().decode())


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--queue", default="http://localhost:8081")
    parser.add_argument("--manifest", default="manifest_full.json")
    parser.add_argument("--snapshot", default="jobs_snapshot.json")
    parser.add_argument("--summary", default="summary.json")
    parser.add_argument("--interval", type=float, default=3.0)
    parser.add_argument("--timeout", type=float, default=1800.0)
    args = parser.parse_args()

    manifest = json.load(open(args.manifest))
    batch = manifest["batch_id"]
    ids = [f"{batch}:{j['id']}" for j in manifest["jobs"]]
    # logical overlay id -> (family, language), recovered from the plan itself.
    meta = {}
    for j in manifest["jobs"]:
        plan = j["render_plan"]
        meta[f"{batch}:{j['id']}"] = {
            "template": plan["items"][0]["template_id"],
            "preset": plan["items"][0]["preset_id"],
            "language": plan["language"],
            "text": plan["items"][0].get("text", ""),
        }

    started = time.time()
    latest = {}
    while time.time() - started < args.timeout:
        done = 0
        for job_id in ids:
            try:
                latest[job_id] = get(f"{args.queue}/jobs/{job_id}")
            except urllib.error.HTTPError as err:
                if err.code == 404:
                    continue
                raise
            if latest[job_id].get("state") in TERMINAL:
                done += 1
        print(f"[poll] {done}/{len(ids)} terminal", flush=True)
        if done == len(ids):
            break
        time.sleep(args.interval)

    json.dump(latest, open(args.snapshot, "w"), indent=2, sort_keys=True)

    rows = []
    for job_id in ids:
        job = latest.get(job_id, {})
        artifact = job.get("artifact") or {}
        queued = parse_ts(job.get("queued_at"))
        began = parse_ts(job.get("started_at"))
        ended = parse_ts(job.get("completed_at"))
        rows.append({
            "job_id": job_id,
            "state": job.get("state", "not_submitted"),
            "attempts": job.get("attempts"),
            "template": meta[job_id]["template"],
            "preset": meta[job_id]["preset"],
            "language": meta[job_id]["language"],
            "text": meta[job_id]["text"],
            "queued_at": job.get("queued_at"),
            "started_at": job.get("started_at"),
            "completed_at": job.get("completed_at"),
            "queue_wait_ms": int((began - queued).total_seconds() * 1000) if queued and began else None,
            "render_wall_ms": int((ended - began).total_seconds() * 1000) if began and ended else None,
            "artifact_sha256": artifact.get("artifact_hash") or artifact.get("sha256"),
            "artifact_bytes": artifact.get("size_bytes"),
            "backend": artifact.get("backend"),
            "fail_reason": job.get("fail_reason", ""),
        })

    terminal = [r for r in rows if r["state"] in TERMINAL]
    completed = [r for r in rows if r["state"] == "completed"]
    failed = [r for r in rows if r["state"] in {"failed", "cancelled"}]
    never = [r for r in rows if r["state"] not in TERMINAL]

    total_wall_ms = None
    stamps = [t for t in (parse_ts(r["completed_at"]) for r in completed) if t]
    first_queued = [t for t in (parse_ts(r["queued_at"]) for r in rows) if t]
    if stamps and first_queued:
        total_wall_ms = int((max(stamps) - min(first_queued)).total_seconds() * 1000)

    def stats(values):
        values = [v for v in values if v is not None]
        if not values:
            return None
        values.sort()
        return {
            "count": len(values),
            "min_ms": values[0],
            "p50_ms": values[len(values) // 2],
            "max_ms": values[-1],
            "sum_ms": sum(values),
        }

    per_language = {}
    for lang in sorted({r["language"] for r in rows}):
        subset = [r for r in completed if r["language"] == lang]
        per_language[lang] = {
            "completed": len(subset),
            "render_wall": stats([r["render_wall_ms"] for r in subset]),
            "queue_wait": stats([r["queue_wait_ms"] for r in subset]),
        }

    frames = 120 * len(completed)
    summary = {
        "batch": batch,
        "jobs_total": len(rows),
        "completed": len(completed),
        "failed": len(failed),
        "not_terminal": len(never),
        "total_wall_ms": total_wall_ms,
        "frames_rendered": frames,
        "aggregate_fps": round(frames / (total_wall_ms / 1000.0), 3) if total_wall_ms else None,
        "jobs_per_minute": round(len(completed) / (total_wall_ms / 60000.0), 3) if total_wall_ms else None,
        "render_wall": stats([r["render_wall_ms"] for r in completed]),
        "queue_wait": stats([r["queue_wait_ms"] for r in completed]),
        "backends": sorted({r["backend"] for r in completed if r["backend"]}),
        "per_language": per_language,
        "failures": [{"job_id": r["job_id"], "state": r["state"], "fail_reason": r["fail_reason"]} for r in failed + never],
        "distinct_artifact_hashes": len({r["artifact_sha256"] for r in completed if r["artifact_sha256"]}),
    }
    json.dump(summary, open(args.summary, "w"), indent=2, sort_keys=True)

    print(json.dumps({k: v for k, v in summary.items() if k != "per_language"}, indent=2, sort_keys=True))
    return 0 if not failed and not never else 1


if __name__ == "__main__":
    sys.exit(main())
