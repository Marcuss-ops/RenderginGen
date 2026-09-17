// Package buildinfo is the RenderingGen half of the ONE runtime-identity
// contract.
//
// The worker's /health already advertised renderinggen/chronon/overlay_schema
// versions, but not the things an operator (or an agent) actually needs when a
// canary render must be trusted:
//
//   - which VCS revision the running worker binary came from;
//   - the digest of that binary, so "the code I built" is provable;
//   - which config file the worker loaded (a stale /tmp override looks exactly
//     like the systemd unit's config from the outside);
//   - when the process started, so a suspended/stale worker is obvious.
//
// Without those, certifying a change meant auditing the unit file, the `ps`
// line, the config backups under /etc and the queue's worker table by hand —
// and a stale worker could still claim the canary job.
//
// The JSON document is the SAME contract PipelineGen publishes on /health and
// /ready (see internal/platform/buildinfo). The two modules are separate Go
// modules, so the struct is deliberately duplicated rather than shared
// through a dependency: what must not diverge is the wire shape, and that is
// pinned by tests on both sides.
package buildinfo

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
)

// Build stamps, overridable with -X at link time. They live here (not in
// package main) so every binary in the module shares one stamped identity.
var (
	// Version is the release/tagged version.
	Version = ""
	// GitCommit is the VCS revision the binary was built from.
	GitCommit = ""
	// BuildTime is the RFC3339 build timestamp.
	BuildTime = ""
	// BinarySHA256 lets a build system pre-compute the executable digest.
	BinarySHA256 = ""
)

// shortCommitLen matches git's abbreviated revision length.
const shortCommitLen = 12

var processStartedAt = time.Now().UTC()

var (
	runtimeMu sync.RWMutex
	boot      RuntimeInfo
)

// RuntimeInfo is the bootstrap-supplied half of the identity.
type RuntimeInfo struct {
	ConfigPath string
	Mode       string
	WorkerID   string
}

// SetRuntime records the process-lifecycle identity. Empty arguments keep the
// previous value, so a caller sets only what it owns.
func SetRuntime(info RuntimeInfo) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if path := strings.TrimSpace(info.ConfigPath); path != "" {
		boot.ConfigPath = path
	}
	if mode := strings.TrimSpace(info.Mode); mode != "" {
		boot.Mode = mode
	}
	if id := strings.TrimSpace(info.WorkerID); id != "" {
		boot.WorkerID = id
	}
}

// Runtime returns a copy of the bootstrap-supplied identity.
func Runtime() RuntimeInfo {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return boot
}

// Identity is the JSON document published under the "build" key of /health.
// Every key is always present so scripts can assert fields directly.
type Identity struct {
	Version       string `json:"version"`
	GitCommit     string `json:"git_commit"`
	GitCommitFull string `json:"git_commit_full"`
	GitDirty      bool   `json:"git_dirty"`
	BuildTime     string `json:"build_time"`
	BinarySHA256  string `json:"binary_sha256"`
	BinaryPath    string `json:"binary_path"`
	ConfigPath    string `json:"config_path"`
	Mode          string `json:"mode"`
	WorkerID      string `json:"worker_id"`
	PID           int    `json:"pid"`
	StartedAt     string `json:"started_at"`
	IdentityHash  string `json:"identity_hash"`
}

// Complete reports whether the identity is strong enough to certify a change:
// a version, a VCS revision and a binary digest.
func (i Identity) Complete() bool {
	return strings.TrimSpace(i.Version) != "" &&
		strings.TrimSpace(i.GitCommit) != "" &&
		strings.TrimSpace(i.BinarySHA256) != ""
}

// Digest is the stable short fingerprint of the identity tuple: two workers
// with the same hash are running identical bytes from the same revision with
// the same config.
func (i Identity) Digest() string {
	return shortHash(strings.Join([]string{
		i.Version, i.GitCommitFull, strconv.FormatBool(i.GitDirty),
		i.BuildTime, i.BinarySHA256, i.ConfigPath, i.Mode, i.WorkerID,
	}, "\x00"))
}

// Current assembles the current process identity. It never fails and performs
// no I/O after the memoized executable digest.
func Current() Identity {
	vcsRevision, vcsTime, vcsModified := vcsStamp()

	identity := Identity{
		Version:       nonEmpty(Version, "0.0.0-dev"),
		GitCommitFull: nonEmpty(GitCommit, vcsRevision),
		GitDirty:      vcsModified,
		BuildTime:     nonEmpty(BuildTime, vcsTime),
		BinarySHA256:  nonEmpty(BinarySHA256, executableDigest()),
		BinaryPath:    executablePath(),
		PID:           os.Getpid(),
		StartedAt:     processStartedAt.Format(time.RFC3339),
	}

	boot := Runtime()
	identity.ConfigPath = boot.ConfigPath
	identity.Mode = boot.Mode
	identity.WorkerID = boot.WorkerID

	if identity.GitCommitFull != "" {
		identity.GitCommit = shortCommit(identity.GitCommitFull)
	}
	identity.IdentityHash = identity.Digest()
	return identity
}

// vcsStamp reads the VCS stamps `go build` bakes into the executable inside a
// checkout. Absent metadata stays absent — never guessed.
func vcsStamp() (revision, commitTime string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return "", "", false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = strings.TrimSpace(setting.Value)
		case "vcs.time":
			commitTime = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	return revision, commitTime, modified
}

// executableDigestCache memoizes the executable digest: the file is immutable
// for the process lifetime and hashing it per /health poll would be wasteful.
var executableDigestCache struct {
	once sync.Once
	sum  string
}

// executableDigest returns the SHA-256 of the running executable, or "" when
// it cannot be read. A missing digest is reported as missing rather than
// substituted, so "looks verified, is not" cannot happen.
func executableDigest() string {
	executableDigestCache.once.Do(func() {
		path, err := os.Executable()
		if err != nil || strings.TrimSpace(path) == "" {
			return
		}
		sum, _, err := hashio.File(path)
		if err != nil {
			return
		}
		executableDigestCache.sum = sum
	})
	return executableDigestCache.sum
}

func executablePath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}

func shortHash(value string) string {
	sum, _, err := hashio.Reader(strings.NewReader(value))
	if err != nil {
		return ""
	}
	if len(sum) <= 16 {
		return sum
	}
	return sum[:16]
}

func shortCommit(revision string) string {
	if len(revision) <= shortCommitLen {
		return revision
	}
	return revision[:shortCommitLen]
}

func nonEmpty(primary, fallback string) string {
	if trimmed := strings.TrimSpace(primary); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(fallback)
}
