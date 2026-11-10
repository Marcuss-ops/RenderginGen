#!/usr/bin/env python3
"""Upload the 4 newspaper MP4s (1920x1080, <=10s) + stills + contact sheet
to Google Drive folder 1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS, subfolder
Newspaper_Pages_1920x1080. Pattern: RenderingGen/bin/drive-upload,
as in upload_documentary_presets_to_drive.py / upload_dof_demo_to_drive.py.

Credentials and uploader output handling are shared with the other UploadDrive
scripts in ``drive_common.py``. No developer home path is baked in.
"""
import glob
import json
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
BASE_DIR = HERE.parents[2]
sys.path.insert(0, str(HERE.parent))

from drive_common import (  # noqa: E402  (path insertion must precede the import)
    UPLOAD_PASS,
    config_paths,
    parse_upload_output,
    run_upload,
    sha256_file,
)

UPLOADER = BASE_DIR / "RenderingGen" / "bin" / "drive-upload"
CREDS, TOKEN = config_paths()
DRIVE_FOLDER = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
SUBFOLDER = "Newspaper_Pages_1920x1080"

FILES = sorted(
    glob.glob(str(HERE / "*.mp4"))
    + glob.glob(str(HERE / "*.png"))
    + glob.glob(str(HERE / "verification.tsv"))
    + glob.glob(str(HERE / "README.md"))
)


def upload(local_path: Path) -> dict:
    name = local_path.name
    digest = sha256_file(local_path)
    size = local_path.stat().st_size
    print(f"[UPLOAD] {name} ({size / 1024:.0f} KB)...", flush=True)
    t0 = time.time()
    rc, out = run_upload(
        UPLOADER, local_path, CREDS, TOKEN, DRIVE_FOLDER, SUBFOLDER, name, digest
    )
    if rc == 0 and UPLOAD_PASS in out:
        fields = parse_upload_output(out)
        link = fields.get("drive_url", "")
        print(f"  -> SUCCESS ({time.time() - t0:.1f}s): {link}", flush=True)
        return {
            "name": name,
            "local_path": str(local_path.relative_to(BASE_DIR)),
            "drive_file_id": fields.get("drive_file_id", ""),
            "drive_url": link,
            "drive_parent_id": fields.get("drive_parent_id", ""),
            "sha256": digest,
            "bytes": size,
        }
    print(f"  -> FAILED rc={rc}: {out[:500]}", flush=True)
    return {"name": name, "error": out[:500]}


def main() -> int:
    results = [upload(path) for path in FILES]
    ok = [result for result in results if "drive_file_id" in result]
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
    with (HERE / "upload_manifest.json").open("w") as handle:
        json.dump(manifest, handle, indent=2)
    with (HERE / "upload.log").open("w") as handle:
        for result in results:
            handle.write(
                f"{result.get('drive_url') or result.get('error', '')}  {result['name']}\n"
            )
    print(f"\nUPLOADED {len(ok)}/{len(results)} -> {manifest['drive_folder_url']}")
    for result in ok:
        print(f"  {result['name']}: {result['drive_url']}")
    return 0 if len(ok) == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
