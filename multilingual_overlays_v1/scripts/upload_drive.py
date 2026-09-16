#!/usr/bin/env python3
"""Publish the certified multilingual overlays to Google Drive.

Chain of custody per file:

  queue artifact_url -> object-store bytes (SHA-256 verified on download)
                     -> drive-upload -sha256 <artifact_hash> (provider must
                        account for every byte or the upload is refused)

Layout on Drive: <target folder>/<language>/<overlay>__<language>.mp4, so the
ten language variants of one overlay sit together and the folder mirrors the
matrix.

Nothing is uploaded twice: a job that is not `completed` is reported as
NOT UPLOADED and never silently skipped.
"""
import argparse
import json
import os
import subprocess
import sys
import urllib.request

import hashlib


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--snapshot", default="jobs_snapshot_run2.json")
    parser.add_argument("--manifest", default="manifest_run2.json")
    parser.add_argument("--stage", default="artifacts")
    parser.add_argument("--uploader", default="drive-upload")
    parser.add_argument("--credentials", required=True)
    parser.add_argument("--token", required=True)
    parser.add_argument("--folder", default="1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS")
    parser.add_argument("--report", default="upload_report.json")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--only", default="",
                        help="comma-separated job-id fragments; retry just those (merged into the report)")
    args = parser.parse_args()

    snapshot = json.load(open(args.snapshot))
    manifest = json.load(open(args.manifest))
    os.makedirs(args.stage, exist_ok=True)

    # A retry must not re-upload the whole matrix: results are merged by job id
    # so the report always describes every job of the batch exactly once.
    previous = {}
    if args.only and os.path.exists(args.report):
        for row in json.load(open(args.report)):
            previous[row["job_id"]] = row
    wanted = [f for f in args.only.split(",") if f]

    jobs = [j for j in manifest["jobs"] if not wanted or any(f in j["id"] for f in wanted)]
    results = []
    uploaded = 0
    for job in jobs:
        job_id = f"{manifest['batch_id']}:{job['id']}"
        body = snapshot.get(job_id, {})
        state = body.get("state")
        language = job["render_plan"]["language"]
        artifact = body.get("artifact") or {}
        entry = {"job_id": job_id, "language": language, "state": state}

        if state != "completed" or not artifact.get("artifact_url"):
            entry["result"] = "NOT_UPLOADED"
            results.append(entry)
            print(f"SKIP {job_id} state={state}", flush=True)
            continue

        target_hash = artifact["artifact_hash"]
        local = os.path.join(args.stage, f"{job['id']}.mp4")
        if not (os.path.exists(local) and sha256_file(local) == target_hash):
            with urllib.request.urlopen(artifact["artifact_url"], timeout=120) as resp, open(local, "wb") as out:
                out.write(resp.read())
        got = sha256_file(local)
        if got != target_hash:
            entry["result"] = "DOWNLOAD_HASH_MISMATCH"
            entry["expected"] = target_hash
            entry["got"] = got
            results.append(entry)
            print(f"FAIL {job_id} download hash mismatch", flush=True)
            continue
        entry["local_path"] = local
        entry["sha256"] = got
        entry["bytes"] = artifact.get("size_bytes")

        if args.dry_run:
            entry["result"] = "DRY_RUN"
            results.append(entry)
            continue

        cmd = [
            args.uploader,
            "-credentials", args.credentials,
            "-token", args.token,
            "-folder", args.folder,
            "-subfolder", language,
            "-file", local,
            "-name", f"{job['id']}.mp4",
            "-sha256", target_hash,
        ]
        proc = subprocess.run(cmd, capture_output=True, text=True)
        entry["upload_stdout"] = proc.stdout.strip()
        if proc.returncode == 0 and proc.stdout.startswith("DRIVE_UPLOAD_PASS"):
            uploaded += 1
            entry["result"] = "UPLOADED"
            for field in proc.stdout.split():
                if field.startswith("id="):
                    entry["drive_file_id"] = field[3:]
                elif field.startswith("link="):
                    entry["drive_link"] = field[5:]
        else:
            entry["result"] = "UPLOAD_FAILED"
            entry["upload_stderr"] = proc.stderr.strip()[:400]
        results.append(entry)
        print(f"{entry['result']:14s} {job_id} -> {entry.get('drive_link', '')}", flush=True)

    merged = list(results)
    if wanted:
        updated = {row["job_id"] for row in results}
        merged += [row for job_id, row in previous.items() if job_id not in updated]
        merged.sort(key=lambda row: row["job_id"])
    json.dump(merged, open(args.report, "w"), indent=2, sort_keys=True)
    ok = sum(1 for r in merged if r["result"] in {"UPLOADED", "DRY_RUN"})
    print(f"\nuploaded={uploaded}/{len(jobs)} this run; report total ok={ok}/{len(merged)} report={args.report}")
    failed = [r for r in merged if r["result"] not in {"UPLOADED", "DRY_RUN"}]
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
