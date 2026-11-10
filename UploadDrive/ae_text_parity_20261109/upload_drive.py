#!/usr/bin/env python3
"""Upload the AE text-animator parity demo bundle to Google Drive.

The bundle is published with the project's content-addressed uploader
(``RenderingGen/bin/drive-upload``) into a dedicated subfolder of the shared
render folder. Credentials resolve exactly like the other UploadDrive scripts:
``VELOX_CONFIG_DIR``, then ``$XDG_CONFIG_HOME/velox``, then ``~/.config/velox``.

Usage:
    VELOX_CONFIG_DIR=/path/to/velox python3 upload_drive.py
"""
from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
UPLOAD_DIR = HERE.parent
BASE = HERE.parents[2]

sys.path.insert(0, str(UPLOAD_DIR))
from drive_common import (  # noqa: E402  (path insertion must precede the import)
    config_paths,
    parse_upload_output,
    run_upload,
    sha256_file,
)

UPLOADER = BASE / "RenderingGen" / "bin" / "drive-upload"
PARENT_FOLDER = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
SUBFOLDER = "AE_Text_Parity_20261109"
GENERATOR = BASE / "ChrononTemplate/tools/text_animators/build_ae_parity_text_demos.py"


def bundle_files() -> list[Path]:
    """Production artifacts only: no debug plans or intermediate frames."""
    files: list[Path] = []
    files += sorted((HERE / "videos").glob("*.mp4"))
    files += sorted((HERE / "videos").glob("*.timing.json"))
    files += sorted((HERE / "stills").glob("*_early.png"))
    files += sorted((HERE / "stills").glob("*_mid.png"))
    files += sorted((HERE / "stills").glob("*_late.png"))
    files += sorted((HERE / "stills/audit").glob("*_contact.png"))
    files += sorted((HERE / "plans").glob("[0-9][0-9]_*.plan.json"))
    files += sorted((HERE / "logs").glob("*.log"))
    files += [HERE / "MANIFEST.json", GENERATOR]
    missing = [str(path) for path in files if not path.is_file()]
    if missing:
        raise SystemExit("missing bundle files: " + ", ".join(missing))
    return files


def upload(path: Path) -> dict:
    """Upload one file, retrying once, and return its local + Drive metadata."""
    credentials, token = config_paths()
    for required in (UPLOADER, credentials, token):
        if not required.is_file():
            raise SystemExit(f"missing upload requirement: {required}")

    digest = sha256_file(path)
    last_error = ""
    for _ in range(2):
        try:
            code, output = run_upload(
                UPLOADER,
                path,
                credentials,
                token,
                PARENT_FOLDER,
                SUBFOLDER,
                path.name,
                digest,
                timeout=240,
            )
        except subprocess.TimeoutExpired:
            last_error = "timeout"
            continue
        fields = parse_upload_output(output)
        if code == 0 and fields.get("drive_file_id"):
            return {
                "local_path": str(path.relative_to(BASE)),
                "name": path.name,
                "sha256": digest,
                "bytes": path.stat().st_size,
                **fields,
            }
        last_error = output.strip()[-400:]
    raise RuntimeError(f"upload failed for {path.name}: {last_error}")


def main() -> int:
    files = bundle_files()
    entries = []
    for index, path in enumerate(files, 1):
        entry = upload(path)
        entries.append(entry)
        print(f"[{index}/{len(files)}] {path.name} -> {entry['drive_url']}", flush=True)

    subfolder_id = entries[0].get("drive_parent_id", PARENT_FOLDER)
    manifest = {
        "schema": "chronon.ae-text-parity.drive-delivery.v1",
        "date": "2026-11-09",
        "notes": (
            "Character Value (seven selector-scoped per-glyph animators, "
            "ADR-037), Character Alignment, Anchor Point Grouping and the V3 "
            "text transitions Typewriter, Scramble and Morph; all rendered "
            "with Vulkan in require_gpu_native mode, 1920x1080, 30 fps, "
            "180 frames."
        ),
        "parent_folder_id": PARENT_FOLDER,
        "drive_subfolder": SUBFOLDER,
        "drive_folder_id": subfolder_id,
        "drive_folder_url": f"https://drive.google.com/drive/folders/{subfolder_id}",
        "file_count": len(entries),
        "files": entries,
    }
    local_manifest = HERE / "upload_manifest.json"
    local_manifest.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    uploaded_manifest = upload(local_manifest)
    manifest["upload_manifest"] = uploaded_manifest
    local_manifest.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    print(f"FOLDER {manifest['drive_folder_url']}")
    print(f"MANIFEST {uploaded_manifest['drive_url']}")
    print(f"UPLOADED {len(entries)} files")
    return 0


if __name__ == "__main__":
    sys.exit(main())
