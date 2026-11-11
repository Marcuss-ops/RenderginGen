#!/usr/bin/env python3
"""Upload the validated 2026-11-11 overlay bundle to Google Drive.

Credentials resolve like the other UploadDrive scripts: VELOX_CONFIG_DIR,
then $XDG_CONFIG_HOME/velox, then ~/.config/velox.
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
SUBFOLDER = "Overlay_Batches_20261111"


def bundle_files() -> list[Path]:
    files = sorted((HERE / "videos").glob("*.mp4"))
    files += sorted((HERE / "contacts").glob("*.png"))
    files += [HERE / "MANIFEST.json", HERE / "README.md"]
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
    last_error = ""
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
        last_error = f"rc={code} {output[-800:]}"
        if attempt == 0:
            print(f"[retry] {path.name}: {last_error[:300]}", flush=True)
    raise SystemExit(f"upload failed for {path.name}: {last_error}")


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
        json.dumps(output, indent=2, ensure_ascii=False) + "\n")
    print("DRIVE_UPLOADS_ALL_PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
