package buildinfo

import (
	"encoding/json"
	"strings"
	"testing"
)

// identityJSONKeys is the wire contract shared with PipelineGen's
// internal/platform/buildinfo. Both modules publish the same document on
// /health; a divergence here would silently break the certifier that compares
// a PipelineGen server against a RenderingGen worker.
var identityJSONKeys = []string{
	"version", "git_commit", "git_commit_full", "git_dirty", "build_time",
	"binary_sha256", "binary_path", "config_path", "mode", "worker_id",
	"pid", "started_at", "identity_hash",
}

// TestIdentity_JSONContractParity pins the shared wire shape.
func TestIdentity_JSONContractParity(t *testing.T) {
	raw, err := json.Marshal(Current())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range identityJSONKeys {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("identity JSON is missing the shared key %q: %s", key, raw)
		}
	}
}

// TestCurrent_IsAlwaysAssembled: a field the process cannot determine is
// empty, never invented, and the document is always produced.
func TestCurrent_IsAlwaysAssembled(t *testing.T) {
	identity := Current()

	if identity.Version == "" {
		t.Error("version must always fall back to a value")
	}
	if identity.PID <= 0 {
		t.Errorf("pid = %d, want > 0", identity.PID)
	}
	if identity.StartedAt == "" {
		t.Error("started_at must always be populated")
	}
	if identity.IdentityHash == "" {
		t.Error("identity_hash must always be derived")
	}
	if identity.BinarySHA256 == "" {
		t.Error("the running test binary must be hashable")
	}
}

// TestSetRuntime_FeedsIdentity is the bootstrap contract that lets an operator
// answer "which config is this worker reading?" from /health alone.
func TestSetRuntime_FeedsIdentity(t *testing.T) {
	previous := Runtime()
	t.Cleanup(func() { SetRuntime(previous) })

	SetRuntime(RuntimeInfo{ConfigPath: "/etc/renderinggen/renderinggen.yaml", Mode: "renderinggen-worker", WorkerID: "w-9"})

	identity := Current()
	if identity.ConfigPath != "/etc/renderinggen/renderinggen.yaml" {
		t.Errorf("config_path = %q", identity.ConfigPath)
	}
	if identity.WorkerID != "w-9" {
		t.Errorf("worker_id = %q", identity.WorkerID)
	}
	if identity.Mode != "renderinggen-worker" {
		t.Errorf("mode = %q", identity.Mode)
	}
}

// TestDigest_IsFieldSensitive makes the one-string compare trustworthy.
func TestDigest_IsFieldSensitive(t *testing.T) {
	base := Identity{Version: "1.0.0", GitCommitFull: "abcdef012345", BinarySHA256: "aa", ConfigPath: "/etc/a.yaml"}

	if base.Digest() != base.Digest() {
		t.Error("digest must be deterministic")
	}
	changed := base
	changed.BinarySHA256 = "bb"
	if base.Digest() == changed.Digest() {
		t.Error("a different binary digest must change identity_hash")
	}
	if len(base.Digest()) != 16 {
		t.Errorf("identity_hash length = %d, want 16", len(base.Digest()))
	}
}

// TestComplete_RequiresRealStamps pins the certifier gate.
func TestComplete_RequiresRealStamps(t *testing.T) {
	if (Identity{Version: "1", GitCommit: "a", BinarySHA256: "b"}).Complete() != true {
		t.Error("a fully stamped identity must be complete")
	}
	if (Identity{Version: "1", GitCommit: "a"}).Complete() != false {
		t.Error("an identity without a binary digest must not be complete")
	}
}

func TestShortCommit(t *testing.T) {
	if got := shortCommit("a1b2c3d4e5f6071829"); got != "a1b2c3d4e5f6" {
		t.Errorf("shortCommit = %q", got)
	}
	if got := shortCommit("abc"); got != "abc" {
		t.Errorf("short revision must be unchanged, got %q", got)
	}
}
