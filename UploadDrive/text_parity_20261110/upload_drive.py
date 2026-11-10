#!/usr/bin/env python3
"""Upload the 1080p text-parity demo bundle to Google Drive.

Credentials resolve like the other UploadDrive scripts: VELOX_CONFIG_DIR,
then $XDG_CONFIG_HOME/velox, then ~/.config/velox. On this host the credentials
live in /home/pierone/.config/velox, so run with:

    VELOX_CONFIG_DIR=/home/pierone/.config/velox python3 upload_drive.py
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
UPLOAD_DIR = HERE.parent
BASE = HERE.parents[2]
sys.path.insert(0, str(UPLOAD_DIR))

from drive_common import (  # noqa: E402
    config_paths,
    parse_upload_output,
    run_upload,
    sha256_file,
)

UPLOADER = BASE / "RenderingGen/bin/drive-upload"
PARENT_FOLDER = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
SUBFOLDER = "Text_Parity_Subpixel_20261110"


def bundle_files() -> list[Path]:
    files = sorted((HERE / "videos").glob("*.mp4"))
    files += sorted((HERE / "stills").glob("*.png"))
    files += sorted((HERE / "plans").glob("*.plan.json"))
    files += sorted((HERE / "logs").glob("*.log"))
    files += [HERE / "MANIFEST.json", HERE / "README.md",
              HERE / "make_plans.py", HERE / "render_bundle.py",
              HERE / "plans.json"]
    missing = [str(path) for path in files if not path.is_file()]
    if missing:
        raise SystemExit("missing bundle files: " + ", ".join(missing))
    return files


def upload(path: Path) -> dict:
    credentials, token = config_paths()
    for required in (UPLOADER, credentials, token):
        if not required.is_file():
            raise SystemExit(f"missing upload requirement: {required}")
    digest = sha256_file(path)
    for attempt in range(2):
        code, output = run_upload(
            UPLOADER, path, credentials, token, PARENT_FOLDER, SUBFOLDER,
            path.name, digest, timeout=900)
        if code == 0 and "DRIVE_UPLOAD_PASS" in output:
            fields = parse_upload_output(output)
            return {
                "local_path": str(path.relative_to(BASE)),
                "name": path.name,
                "sha256": digest,
                "bytes": path.stat().st_size,
                **fields,
            }
        if attempt == 0:
            print(f"[retry] {path.name}: rc={code} {output[:300]}",
                  flush=True)
    raise SystemExit(f"upload failed for {path.name}: rc={code} {output}")


def main() -> int:
    manifest = json.loads((HERE / "MANIFEST.json").read_text())
    uploaded = []
    for path in bundle_files():
        print(f"[upload] {path.name}", flush=True)
        uploaded.append(upload(path))
    output = {
        "schema": manifest["schema"] + ".upload",
        "parent_folder_id": PARENT_FOLDER,
        "drive_subfolder": SUBFOLDER,
        "file_count": len(uploaded),
        "files": uploaded,
    }
    (HERE / "upload_manifest.json").write_text(
        json.dumps(output, indent=2) + "\n")
    print("DRIVE_UPLOADS_ALL_PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
