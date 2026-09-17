// Package buildinfo is the object store's half of the ONE runtime-identity
// contract.
//
// The store is the third service in the render path, and until now it was the
// only one that published NOTHING about itself: /health answered
// {"status":"ok"} and nothing else. That made the store the weakest link in
// exactly the question the other two services just learned to answer — "is the
// process I am talking to running the code I deployed?" — because an operator
// can now read the revision and digest of the PipelineGen server and the
// RenderingGen worker but has to guess which object store binary is behind
// :9000, on which revision, and whether it is even a build that verifies
// content addresses at all.
//
// The JSON document is the SAME contract PipelineGen publishes on /health and
// /ready (internal/platform/buildinfo) and the RenderingGen worker publishes on
// /health (renderinggen/internal/buildinfo): identical field names, identical
// fallback rules, identical identity_hash derivation. The three services live
// in three separate Go modules, so the struct is deliberately duplicated rather
// than shared through a dependency — what must not diverge is the WIRE SHAPE,
// and that is pinned by a key-parity test in each module (here, by
// TestIdentityJSONContractParityWithSiblingModules, which reads the sibling
// struct tags off disk). Sharing it through a module dependency would make each
// service's build depend on another service's release cadence for a document
// that only ever needs to agree on names.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Build stamps, overridable with -X at link time. They live here (not in
// package main) so the build system has one set of symbols to stamp, and so an
// unstamped binary is visibly unstamped: the fallbacks below are honest
// placeholders, never a claim about which revision this is.
var (
	// Version is the release/tagged version.
	Version = ""
	// GitCommit is the VCS revision the binary was built from.
	GitCommit = ""
	// BuildTime is the RFC3339 build timestamp.
	BuildTime = ""
	// BinarySHA256 lets a build system pre-compute the executable digest when
	// the binary cannot hash itself.
	BinarySHA256 = ""
)

// shortCommitLen matches git's abbreviated revision length, so a value copied
// from /health pastes straight into `git show`.
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
// Every key is always present so a probe can assert fields directly instead of
// branching on key absence.
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
// a version, a VCS revision and a binary digest. Without all three, "is this
// the binary I built?" is unanswerable.
func (i Identity) Complete() bool {
	return strings.TrimSpace(i.Version) != "" &&
		strings.TrimSpace(i.GitCommit) != "" &&
		strings.TrimSpace(i.BinarySHA256) != ""
}

// Digest is the stable short fingerprint of the identity tuple, derived from
// the tuple and never from the clock, so two processes with the same string are
// running identical bytes from the same revision.
func (i Identity) Digest() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		i.Version, i.GitCommitFull, strconv.FormatBool(i.GitDirty),
		i.BuildTime, i.BinarySHA256, i.ConfigPath, i.Mode, i.WorkerID,
	}, "\x00")))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) <= 16 {
		return encoded
	}
	return encoded[:16]
}

// Current assembles the current process identity. It never fails and performs
// no I/O after the memoized executable digest, so it is safe on a per-request
// health handler.
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
// for the process lifetime, and hashing a multi-MiB binary on every /health
// poll would make the endpoint expensive for no new information.
var executableDigestCache struct {
	once sync.Once
	sum  string
}

// executableDigest returns the SHA-256 of the running executable, or "" when it
// cannot be read. A missing digest is reported as missing rather than
// substituted, so "looks verified, is not" cannot happen.
func executableDigest() string {
	executableDigestCache.once.Do(func() {
		path, err := os.Executable()
		if err != nil || strings.TrimSpace(path) == "" {
			return
		}
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		hasher := sha256.New()
		if _, err := io.Copy(hasher, f); err != nil {
			return
		}
		executableDigestCache.sum = hex.EncodeToString(hasher.Sum(nil))
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
