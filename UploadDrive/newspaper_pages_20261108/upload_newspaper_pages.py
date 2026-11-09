#!/usr/bin/env python3
"""Upload the 4 newspaper MP4s (1920x1080, <=10s) + stills + contact sheet
to Google Drive folder 1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS, subfolder
Newspaper_Pages_1920x1080. Pattern: RenderingGen/bin/drive-upload,
as in upload_documentary_presets_to_drive.py / upload_dof_demo_to_drive.py.
"""
import glob
import hashlib
import json
import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
BASE_DIR = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
UPLOADER = os.path.join(BASE_DIR, "RenderingGen/bin/drive-upload")
CREDS = "/home/pierone/.config/velox/credentials.json"
TOKEN = "/home/pierone/.config/velox/token.json"
DRIVE_FOLDER = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
SUBFOLDER = "Newspaper_Pages_1920x1080"

FILES = sorted(
    glob.glob(os.path.join(HERE, "*.mp4"))
    + glob.glob(os.path.join(HERE, "*.png"))
    + glob.glob(os.path.join(HERE, "verification.tsv"))
    + glob.glob(os.path.join(HERE, "README.md"))
)


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def upload(local_path):
    name = os.path.basename(local_path)
    cmd = [
        UPLOADER, "-credentials", CREDS, "-token", TOKEN,
        "-folder", DRIVE_FOLDER, "-subfolder", SUBFOLDER,
        "-file", local_path, "-name", name,
        "-sha256", sha256(local_path),
    ]
    print(f"[UPLOAD] {name} ({os.path.getsize(local_path)/1024:.0f} KB)...", flush=True)
    t0 = time.time()
    res = subprocess.run(cmd, capture_output=True, text=True)
    out = (res.stdout.strip() + " " + res.stderr.strip())
    if res.returncode == 0 and "DRIVE_UPLOAD_PASS" in out:
        fid = link = parent = ""
        for tok in out.split():
            if tok.startswith("id="):
                fid = tok[3:]
            elif tok.startswith("link="):
                link = tok[5:]
            elif tok.startswith("parent="):
                parent = tok[7:]
        print(f"  -> SUCCESS ({time.time()-t0:.1f}s): {link}", flush=True)
        return {"name": name, "local_path": os.path.relpath(local_path, BASE_DIR),
                "drive_file_id": fid, "drive_url": link, "drive_parent_id": parent,
                "sha256": sha256(local_path), "bytes": os.path.getsize(local_path)}
    print(f"  -> FAILED rc={res.returncode}: {out[:500]}", flush=True)
    return {"name": name, "error": out[:500]}


def main():
    results = [upload(p) for p in FILES]
    ok = [r for r in results if "drive_file_id" in r]
    manifest = {
        "schema": "chronon.newspaper-pages.drive-delivery.v1",
        "date": "2026-11-08",
        "spec": "1920x1080, 30fps, H.264, <=10s per video",
        "parent_folder_id": DRIVE_FOLDER,
        "drive_subfolder": SUBFOLDER,
        "drive_folder_url": f"https://drive.google.com/drive/folders/{DRIVE_FOLDER}",
        "file_count": len(ok),
        "files": ok,
        "manifest_note": "This manifest was uploaded after the media files; its own Drive ID is not listed here.",
    }
    with open(os.path.join(HERE, "upload_manifest.json"), "w") as f:
        json.dump(manifest, f, indent=2)
    with open(os.path.join(HERE, "upload.log"), "w") as f:
        for r in results:
            f.write(f"{r.get('drive_url') or r.get('error','')}  {r['name']}\n")
    print(f"\nUPLOADED {len(ok)}/{len(results)} -> {manifest['drive_folder_url']}")
    for r in ok:
        print(f"  {r['name']}: {r['drive_url']}")
    return 0 if len(ok) == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
