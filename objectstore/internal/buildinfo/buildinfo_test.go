package buildinfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// identityJSONKeys is the wire contract shared with PipelineGen's
// internal/platform/buildinfo and the RenderingGen worker's
// renderinggen/internal/buildinfo. All three publish the same document on
// /health; a divergence here would silently break whatever compares a
// PipelineGen server against a RenderingGen worker against an object store.
var identityJSONKeys = []string{
	"version", "git_commit", "git_commit_full", "git_dirty", "build_time",
	"binary_sha256", "binary_path", "config_path", "mode", "worker_id",
	"pid", "started_at", "identity_hash",
}

// TestIdentityJSONContract is the LOCAL half of the parity check.
func TestIdentityJSONContract(t *testing.T) {
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

// TestIdentityJSONContractParityWithSiblingModules is the ENFORCED half. The
// three buildinfo packages are deliberately duplicated across three Go modules
// (see the package doc), and until now "the wire shape must not diverge" was a
// comment in three files rather than a gate — the exact shape of claim this
// repository replaces with an executable check.
//
// It reads the siblings' ACTUAL struct tags off disk and compares them with
// this module's, so renaming a field in any one of the three services fails
// here instead of surfacing as an unparseable /health document during an
// incident. The check skips (never fails) when a sibling is not checked out, so
// a standalone objectstore checkout stays green and the absence stays visible.
func TestIdentityJSONContractParityWithSiblingModules(t *testing.T) {
	want := localIdentityJSONKeys()
	if len(want) == 0 {
		t.Fatal("this module's Identity declared no json keys; the contract under test is empty")
	}

	compared := 0
	for _, sibling := range siblingBuildInfoSources() {
		source, err := os.ReadFile(sibling.path)
		if err != nil {
			t.Logf("skipping %s: %v (sibling not checked out)", sibling.name, err)
			continue
		}
		got := identityKeysOf(string(source))
		if len(got) == 0 {
			t.Errorf("%s: found no identity json tags; the parser or the struct shape changed", sibling.name)
			continue
		}
		compared++
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s publishes a DIFFERENT identity document:\n this module: %v\n %s: %v",
				sibling.name, want, sibling.name, got)
		}
	}
	// A test that silently compares nothing is not a gate. In the full
	// workspace both siblings are present and this assert is the proof the
	// comparison ran; in a standalone objectstore checkout the absence is
	// reported as a SKIP (visible in the runner output) rather than a pass.
	if compared == 0 {
		t.Skip("no sibling buildinfo checked out; the cross-module wire parity is not verifiable here")
	}
	t.Logf("wire parity verified against %d sibling module(s) with %d shared keys", compared, len(want))
}

// TestSiblingWireParityGateIsNotVacuous fails when the gate's own plumbing
// silently resolves to nothing in a layout that DOES contain the siblings. It
// exists because a parity check that skips in the very environment it is meant
// to protect is worse than no check: it reports green while enforcing nothing.
func TestSiblingWireParityGateIsNotVacuous(t *testing.T) {
	sources := siblingBuildInfoSources()
	if len(sources) == 0 {
		t.Fatal("the sibling source list resolved to empty; the parity gate can never fire")
	}
	for _, sibling := range sources {
		if _, err := os.Stat(sibling.path); err != nil {
			continue // genuinely absent: the parity test skips and says so
		}
		if keys := identityKeysOf(readFileString(t, sibling.path)); len(keys) != len(localIdentityJSONKeys()) {
			t.Errorf("%s: parsed %d keys from a present file, want %d; the gate is not reading what it thinks",
				sibling.name, len(keys), len(localIdentityJSONKeys()))
		}
	}
}

// TestCurrentIsAlwaysAssembled pins the no-fake-availability contract: the
// document is always produced, and a fact the process cannot determine is
// empty rather than invented.
func TestCurrentIsAlwaysAssembled(t *testing.T) {
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
	if got := len(identity.BinarySHA256); got != 64 {
		t.Errorf("binary_sha256 length = %d, want 64 (the running test binary must be hashable)", got)
	}
	if identity.BinaryPath == "" {
		t.Error("binary_path must be populated when the executable is resolvable")
	}
}

// TestSetRuntimeFeedsIdentity is the bootstrap contract that lets an operator
// answer "which store build, which instance?" from /health alone.
func TestSetRuntimeFeedsIdentity(t *testing.T) {
	previous := Runtime()
	t.Cleanup(func() { SetRuntime(previous) })

	SetRuntime(RuntimeInfo{Mode: "objectstore", WorkerID: "store-1"})

	identity := Current()
	if identity.Mode != "objectstore" {
		t.Errorf("mode = %q", identity.Mode)
	}
	if identity.WorkerID != "store-1" {
		t.Errorf("worker_id = %q", identity.WorkerID)
	}
}

// TestSetRuntimeEmptyLeavesPreviousValue: an empty argument does not erase
// identity a previous bootstrap step owned.
func TestSetRuntimeEmptyLeavesPreviousValue(t *testing.T) {
	previous := Runtime()
	t.Cleanup(func() { SetRuntime(previous) })

	SetRuntime(RuntimeInfo{Mode: "objectstore", WorkerID: "store-1"})
	SetRuntime(RuntimeInfo{ConfigPath: "  ", Mode: "", WorkerID: ""})

	if got := Runtime(); got.Mode != "objectstore" || got.WorkerID != "store-1" {
		t.Errorf("empty SetRuntime overwrote identity: %+v", got)
	}
}

// TestDigestIsStableAndFieldSensitive makes the one-string compare trustworthy.
func TestDigestIsStableAndFieldSensitive(t *testing.T) {
	base := Identity{Version: "0.1.0", GitCommitFull: "abcdef012345", BinarySHA256: "aa", Mode: "objectstore"}

	if base.Digest() != base.Digest() {
		t.Error("digest must be deterministic for the same tuple")
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

// TestCompleteRequiresRealStamps pins the certifier gate.
func TestCompleteRequiresRealStamps(t *testing.T) {
	cases := []struct {
		name     string
		identity Identity
		want     bool
	}{
		{"all stamped", Identity{Version: "0.1.0", GitCommit: "abc123", BinarySHA256: "deadbeef"}, true},
		{"no revision", Identity{Version: "0.1.0", BinarySHA256: "deadbeef"}, false},
		{"no digest", Identity{Version: "0.1.0", GitCommit: "abc123"}, false},
		{"no version", Identity{GitCommit: "abc123", BinarySHA256: "deadbeef"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.identity.Complete(); got != tc.want {
				t.Errorf("Complete() = %v, want %v", got, tc.want)
			}
		})
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

func TestNonEmpty(t *testing.T) {
	if got := nonEmpty("  ", "fallback"); got != "fallback" {
		t.Errorf("nonEmpty(blank) = %q", got)
	}
	if got := nonEmpty("value", "fallback"); got != "value" {
		t.Errorf("nonEmpty(value) = %q", got)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

type siblingSource struct {
	name string
	path string
}

// siblingBuildInfoSources resolves the two sibling services' buildinfo files
// from THIS file's location, so the check does not depend on the working
// directory a test runner happened to use.
func siblingBuildInfoSources() []siblingSource {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil
	}
	// …/RenderingGen/objectstore/internal/buildinfo/buildinfo_test.go
	moduleDir := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // …/objectstore
	repoDir := filepath.Dir(moduleDir)                              // …/RenderingGen
	workspaceRoot := filepath.Dir(repoDir)
	return []siblingSource{
		{"renderinggen", filepath.Join(repoDir, "renderinggen", "internal", "buildinfo", "buildinfo.go")},
		{"refactored", filepath.Join(workspaceRoot, "refactored", "internal", "platform", "buildinfo", "buildinfo.go")},
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// localIdentityJSONKeys derives this module's wire keys from the struct itself,
// so the comparison is against the contract as compiled, not against a second
// hand-maintained list.
func localIdentityJSONKeys() []string {
	typ := reflect.TypeOf(Identity{})
	keys := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// identityKeysOf extracts the json tag names of the `type Identity struct`
// declaration in a sibling buildinfo.go. It is deliberately scoped to that one
// declaration: unrelated json tags elsewhere in the file are not part of the
// shared document.
func identityKeysOf(source string) []string {
	start := strings.Index(source, "type Identity struct {")
	if start < 0 {
		return nil
	}
	body := source[start:]
	// The declaration ends at the first line that starts with "}".
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	var keys []string
	for _, line := range strings.Split(body, "\n") {
		idx := strings.Index(line, `json:"`)
		if idx < 0 {
			continue
		}
		rest := line[idx+len(`json:"`):]
		tagEnd := strings.Index(rest, `"`)
		if tagEnd < 0 {
			continue
		}
		name := strings.Split(rest[:tagEnd], ",")[0]
		if name == "" || name == "-" {
			continue
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}
