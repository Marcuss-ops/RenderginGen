"""Stdlib-only coverage for UploadDrive/drive_common.py."""

from __future__ import annotations

import hashlib
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from drive_common import (  # noqa: E402  (path insertion must precede the import)
    config_paths,
    parse_upload_output,
    run_upload,
    sha256_file,
    stable_seed,
    upload_command,
)


class ConfigPathsTest(unittest.TestCase):
    def test_explicit_config_dir_wins(self) -> None:
        with mock.patch.dict(
            os.environ,
            {
                "VELOX_CONFIG_DIR": "/tmp/velox-test",
                "XDG_CONFIG_HOME": "/tmp/ignored",
            },
            clear=False,
        ):
            self.assertEqual(
                config_paths(),
                (
                    Path("/tmp/velox-test/credentials.json"),
                    Path("/tmp/velox-test/token.json"),
                ),
            )

    def test_xdg_config_home_is_the_base(self) -> None:
        with mock.patch.dict(
            os.environ,
            {"XDG_CONFIG_HOME": "/tmp/xdg-test"},
            clear=False,
        ):
            os.environ.pop("VELOX_CONFIG_DIR", None)
            self.assertEqual(
                config_paths(),
                (
                    Path("/tmp/xdg-test/velox/credentials.json"),
                    Path("/tmp/xdg-test/velox/token.json"),
                ),
            )

    def test_home_config_fallback(self) -> None:
        with mock.patch.dict(os.environ, {"HOME": "/tmp/home-test"}, clear=False):
            os.environ.pop("VELOX_CONFIG_DIR", None)
            os.environ.pop("XDG_CONFIG_HOME", None)
            self.assertEqual(
                config_paths(),
                (
                    Path("/tmp/home-test/.config/velox/credentials.json"),
                    Path("/tmp/home-test/.config/velox/token.json"),
                ),
            )


class SeedAndHashTest(unittest.TestCase):
    def test_stable_seed_is_process_independent(self) -> None:
        self.assertEqual(stable_seed("01_parallax.mp4"), stable_seed("01_parallax.mp4"))
        self.assertNotEqual(stable_seed("01_parallax.mp4"), stable_seed("02_spline.mp4"))

    def test_sha256_file(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "payload.bin"
            path.write_bytes(b"abc")
            self.assertEqual(sha256_file(path), hashlib.sha256(b"abc").hexdigest())


class UploadOutputTest(unittest.TestCase):
    def test_parses_drive_fields(self) -> None:
        output = "DRIVE_UPLOAD_PASS id=file-1 link=https://drive.test/x parent=folder-1"
        self.assertEqual(
            parse_upload_output(output),
            {
                "drive_file_id": "file-1",
                "drive_url": "https://drive.test/x",
                "drive_parent_id": "folder-1",
            },
        )

    def test_upload_command_keeps_the_uploader_contract(self) -> None:
        command = upload_command(
            "/bin/drive-upload",
            "/tmp/clip.mp4",
            "/tmp/credentials.json",
            "/tmp/token.json",
            "folder",
            "subfolder",
            "clip.mp4",
            "abc123",
        )
        self.assertEqual(command[0], "/bin/drive-upload")
        self.assertIn("clip.mp4", command)
        self.assertEqual(command[command.index("-sha256") + 1], "abc123")

    def test_run_upload_combines_stdout_and_stderr(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            uploader = Path(temp) / "fake-upload"
            uploader.write_text(
                "#!/usr/bin/env python3\n"
                "import sys\n"
                "print('DRIVE_UPLOAD_PASS id=file-1 link=https://drive.test/x parent=p')\n"
                "print('diagnostic', file=sys.stderr)\n"
            )
            uploader.chmod(0o755)
            rc, output = run_upload(
                uploader,
                "clip.mp4",
                "credentials.json",
                "token.json",
                "folder",
                "subfolder",
                "clip.mp4",
                "abc123",
            )
            self.assertEqual(rc, 0)
            self.assertIn("DRIVE_UPLOAD_PASS", output)
            self.assertIn("diagnostic", output)


if __name__ == "__main__":
    unittest.main()
