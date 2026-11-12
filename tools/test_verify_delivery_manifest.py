import copy
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import verify_delivery_manifest as verifier

VALID = {
    "schema_version": "delivery-manifest.v1",
    "job_id": "job-123",
    "logical_job_id": "garlasco-r151",
    "attempt_id": "attempt-4",
    "status": "SUCCEEDED",
    "artifact": {
        "sha256": "a" * 64,
        "uri": "objectstore://deliveries/garlasco-r151.mp4",
        "size_bytes": 1024,
    },
    "media": {
        "duration_us": 4_000_000,
        "width": 1920,
        "height": 1080,
        "fps_num": 24,
        "fps_den": 1,
        "codec": "h264",
        "frame_count": 96,
    },
    "frame_checks": {
        "first": {"frame_index": 0, "sha256": "b" * 64},
        "middle": {"frame_index": 48, "sha256": "c" * 64},
        "last": {"frame_index": 95, "sha256": "d" * 64},
    },
    "code": {
        "pipelinegen_sha": "e" * 40,
        "renderinggen_sha": "f" * 40,
    },
    "tests": [{"name": "conformance", "status": "PASS"}],
    "approval": {"approved": True, "approved_by": "operator"},
}


class DeliveryManifestTest(unittest.TestCase):
    def test_valid_success_manifest(self):
        self.assertEqual(verifier.validate(copy.deepcopy(VALID)), [])

    def test_missing_sha_is_rejected(self):
        broken = copy.deepcopy(VALID)
        broken["artifact"]["sha256"] = ""
        self.assertTrue(any("artifact.sha256" in error for error in verifier.validate(broken)))

    def test_zero_frames_is_rejected(self):
        broken = copy.deepcopy(VALID)
        broken["media"]["frame_count"] = 0
        self.assertTrue(any("frame_count" in error for error in verifier.validate(broken)))

    def test_missing_frame_check_is_rejected(self):
        broken = copy.deepcopy(VALID)
        del broken["frame_checks"]["last"]
        self.assertTrue(any("frame_checks.last" in error for error in verifier.validate(broken)))

    def test_success_with_failed_test_is_rejected(self):
        broken = copy.deepcopy(VALID)
        broken["tests"] = [{"name": "conformance", "status": "FAIL"}]
        self.assertTrue(any("FAIL test" in error for error in verifier.validate(broken)))

    def test_unapproved_is_rejected(self):
        broken = copy.deepcopy(VALID)
        broken["approval"]["approved"] = False
        self.assertTrue(any("approval.approved" in error for error in verifier.validate(broken)))


if __name__ == "__main__":
    unittest.main()
