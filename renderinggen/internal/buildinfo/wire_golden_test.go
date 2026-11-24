package buildinfo

import (
	"encoding/json"
	"testing"
)

// TestIdentityJSONWireGolden is the worker half of the byte-for-byte parity
// proof. The objectstore module asserts the SAME golden string in
// wire_golden_test.go, so a field rename, reorder or tag change on either side
// fails one of the two modules.
func TestIdentityJSONWireGolden(t *testing.T) {
	const want = `{"version":"1.2.3","git_commit":"abcdef123456","git_commit_full":"abcdef1234567890","git_dirty":true,"build_time":"2026-11-24T00:00:00Z","binary_sha256":"deadbeef","binary_path":"/bin/rg","config_path":"/etc/rg.yaml","mode":"worker","worker_id":"w1","pid":42,"started_at":"2026-11-24T00:00:01Z","identity_hash":"0123456789abcdef"}`
	identity := Identity{
		Version:       "1.2.3",
		GitCommit:     "abcdef123456",
		GitCommitFull: "abcdef1234567890",
		GitDirty:      true,
		BuildTime:     "2026-11-24T00:00:00Z",
		BinarySHA256:  "deadbeef",
		BinaryPath:    "/bin/rg",
		ConfigPath:    "/etc/rg.yaml",
		Mode:          "worker",
		WorkerID:      "w1",
		PID:           42,
		StartedAt:     "2026-11-24T00:00:01Z",
		IdentityHash:  "0123456789abcdef",
	}
	got, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("identity wire bytes changed:\n got: %s\nwant: %s", got, want)
	}
}
