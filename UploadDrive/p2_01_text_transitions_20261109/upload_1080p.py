#!/usr/bin/env python3
"""Upload the P2-01 1920x1080 delivery to the shared Drive folder."""

from __future__ import annotations

import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
BASE = HERE.parents[2]
sys.path.insert(0, str(HERE.parent))

from drive_common import (  # noqa: E402
    config_paths,
    parse_upload_output,
    run_upload,
    sha256_file,
)

UPLOADER = BASE / "RenderingGen" / "bin" / "drive-upload"
PARENT_FOLDER = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
SUBFOLDER = "P2_01_Text_Transitions_1920x1080"
DELIVERY = HERE / "p2_01_text_transitions_1920x1080.mp4"
CONTACT = HERE / "contact_1920x1080.png"
README = HERE / "README.md"
SCRIPT = HERE / "upload_1080p.py"
SOURCE_PLAN = HERE / "p2_01_text_transitions.plan.json"
MANIFEST = HERE / "upload_manifest.json"

FILES = [DELIVERY, CONTACT, README, SCRIPT, SOURCE_PLAN]


def upload(path: Path) -> dict:
    credentials, token = config_paths()
    for required in (UPLOADER, credentials, token):
        if not required.is_file():
            raise SystemExit(f"missing upload requirement: {required}")

    digest = sha256_file(path)
    last_error = ""
    for _ in range(2):
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
        fields = parse_upload_output(output)
        if code == 0 and fields.get("drive_file_id"):
            return {
                "name": path.name,
                "local_path": str(path.relative_to(BASE)),
                "sha256": digest,
                "bytes": path.stat().st_size,
                **fields,
            }
        last_error = output.strip()[-500:]
    raise RuntimeError(f"upload failed for {path.name}: {last_error}")


def main() -> int:
    if not DELIVERY.is_file():
        raise SystemExit(f"missing delivery video: {DELIVERY}")

    entries = []
    for index, path in enumerate(FILES, 1):
        entry = upload(path)
        entries.append(entry)
        print(f"[{index}/{len(FILES)}] {path.name} -> {entry['drive_url']}",
              flush=True)

    subfolder_id = entries[0].get("drive_parent_id", PARENT_FOLDER)
    manifest = {
        "schema": "chronon.p2-text-transitions.drive-delivery.v1",
        "date": "2026-11-09",
        "spec": "1920x1080, 30fps, H.264, <=10s",
        "parent_folder_id": PARENT_FOLDER,
        "drive_subfolder": SUBFOLDER,
        "drive_folder_id": subfolder_id,
        "drive_folder_url": f"https://drive.google.com/drive/folders/{subfolder_id}",
        "source": {
            "name": "p2_01_text_transitions_720p.mp4",
            "canvas": [1280, 720],
            "fps": 30,
            "frames": 120,
            "duration_s": 4.0,
            "sha256": sha256_file(HERE / "p2_01_text_transitions_720p.mp4"),
        },
        "delivery": {
            "name": DELIVERY.name,
            "canvas": [1920, 1080],
            "fps": 30,
            "frames": 120,
            "duration_s": 4.0,
            "codec": "h264",
            "pixel_format": "yuv420p",
            "upscale": "lanczos 1.5x",
            "sha256": sha256_file(DELIVERY),
        },
        "native_attempt": {
            "backend": "software",
            "plan": "p2_01_text_transitions_1920x1080.plan.json",
            "result": "FAIL at frame 15",
            "error": "prepare_text_run: invalid or oversized text surface bounds",
            "note": (
                "The native 1920x1080 plan fails at the 90-degree edge-on "
                "rotation of BEYOND 89. The delivered file is a Lanczos "
                "upscale of the complete 720p source, not a native raster."
            ),
        },
        "file_count": len(entries),
        "files": entries,
    }
    MANIFEST.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    manifest_entry = upload(MANIFEST)
    manifest["upload_manifest"] = manifest_entry
    MANIFEST.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    print(f"FOLDER {manifest['drive_folder_url']}")
    print(f"MANIFEST {manifest_entry['drive_url']}")
    print(f"UPLOADED {len(entries)} files")
    return 0


if __name__ == "__main__":
    sys.exit(main())
