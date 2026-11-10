"""Shared helpers for the UploadDrive delivery scripts.

Both delivery scripts need the same credential paths, the same SHA-256
implementation, the same deterministic seed derivation and the same parsing of
``drive-upload`` output. Keeping those rules here means a change to the
credential location or the uploader contract cannot leave one script behind.

Credential resolution order (no developer home path is baked in):

1. ``VELOX_CONFIG_DIR`` (the directory that contains ``credentials.json`` and
   ``token.json``);
2. ``$XDG_CONFIG_HOME/velox``;
3. ``~/.config/velox``.
"""

from __future__ import annotations

import hashlib
import os
import subprocess
import zlib
from pathlib import Path

UPLOAD_PASS = "DRIVE_UPLOAD_PASS"


def config_paths() -> tuple[Path, Path]:
    """Return the credential and token paths for the current environment."""
    configured = os.environ.get("VELOX_CONFIG_DIR", "").strip()
    if configured:
        root = Path(configured).expanduser()
    else:
        xdg = os.environ.get("XDG_CONFIG_HOME", "").strip()
        base = Path(xdg).expanduser() if xdg else Path.home() / ".config"
        root = base / "velox"
    return root / "credentials.json", root / "token.json"


def sha256_file(path: str | Path) -> str:
    """Return the lower-case SHA-256 hex digest of *path*."""
    digest = hashlib.sha256()
    with Path(path).open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def stable_seed(name: str) -> int:
    """Return a process-independent seed derived from *name*.

    Python's ``hash()`` is salted per process, so using it as a render seed
    makes two runs of the same clip produce different bytes. CRC-32 is stable
    across processes and Python versions, which is all a visual seed needs.
    """
    return zlib.crc32(name.encode("utf-8")) & 0xFFFFFFFF


def upload_command(
    uploader: str | Path,
    local_path: str | Path,
    credentials: str | Path,
    token: str | Path,
    folder: str,
    subfolder: str,
    name: str,
    digest: str,
) -> list[str]:
    """Build the ``drive-upload`` invocation shared by both scripts."""
    return [
        str(uploader),
        "-credentials",
        str(credentials),
        "-token",
        str(token),
        "-folder",
        folder,
        "-subfolder",
        subfolder,
        "-file",
        str(local_path),
        "-name",
        name,
        "-sha256",
        digest,
    ]


def run_upload(
    uploader: str | Path,
    local_path: str | Path,
    credentials: str | Path,
    token: str | Path,
    folder: str,
    subfolder: str,
    name: str,
    digest: str,
    timeout: float | None = None,
) -> tuple[int, str]:
    """Run ``drive-upload`` and return ``(exit_code, combined_output)``."""
    result = subprocess.run(
        upload_command(
            uploader,
            local_path,
            credentials,
            token,
            folder,
            subfolder,
            name,
            digest,
        ),
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )
    output = "\n".join(
        part.strip() for part in (result.stdout, result.stderr) if part.strip()
    )
    return result.returncode, output


def parse_upload_output(output: str) -> dict[str, str]:
    """Extract the uploader's ``id=``/``link=``/``parent=`` fields."""
    fields: dict[str, str] = {}
    prefixes = {
        "id=": "drive_file_id",
        "link=": "drive_url",
        "parent=": "drive_parent_id",
    }
    for token in output.split():
        for prefix, key in prefixes.items():
            if token.startswith(prefix):
                fields[key] = token[len(prefix):]
    return fields
