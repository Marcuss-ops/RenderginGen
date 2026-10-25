// staging.go — streaming staging for the content-addressed store.
//
// The upload contract this file exists for: an artifact whose bytes are
// FINAL WHILE THEY ARE APPENDED (a fragmented MP4, see the render lane) must
// be uploadable WITHOUT waiting for the producing file to complete, and the
// store must still never install bytes that do not hash to their address.
//
// The staging flow splits the single verified PUT into two steps:
//
//	StagingWrite(session, r)        streams bytes to a PENDING object keyed by
//	                                session and returns their SHA-256 while it
//	                                hashes them (one pass, no reread).
//	StagingFinalize(session, digest) installs the pending bytes under their
//	                                content address ATOMICALLY, but only when
//	                                the caller's digest equals the digest the
//	                                store computed at write time.
//
// The content-addressing invariant is therefore unchanged — `sha256(bytes at
// key) == key` still holds for every visible object — while the caller no
// longer has to know the key before streaming the bytes: the key is DERIVED
// from the stream and asserted by the caller at finalize. A caller that
// asserts the wrong digest gets ErrContentAddressMismatch and the pending
// bytes are discarded, exactly like a rejected PUT.
//
// Pending objects are invisible to Get/Head/Open: staging is a private
// workspace, never a second addressing scheme.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrStagingNotFound is returned when finalize/abort names a session with no
// pending object.
var ErrStagingNotFound = errors.New("staging object not found")

// ErrStagingSession is returned for a session token that is not a safe opaque
// id. The token becomes a file name, so it is restricted to the grammar below
// instead of being escaped.
var ErrStagingSession = errors.New("invalid staging session id")

// stagingSessionGrammar accepts opaque ids (a job/attempt/uuid spelling). It
// deliberately excludes path separators and dots so a session can never
// address anything but its own pending file.
var stagingSessionGrammar = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// stagingDigestPathName and stagingPendingPathName keep the two file-name
// spellings in one place (the grammar above already excludes separators).
const (
	stagingPendingSuffix = ".pending"
	stagingDigestSuffix  = ".sha256"
)

// StagingBackend is the OPTIONAL streaming-staging extension of Backend. A
// backend that does not implement it keeps the single verified PUT contract;
// the HTTP surface reports 501 for the staging routes rather than pretending.
type StagingBackend interface {
	// StagingWrite streams r into a pending object for session and returns
	// the SHA-256 of the streamed bytes. Repeated writes for the same session
	// replace the pending object (the last writer wins; the digest returned is
	// always the one of THIS stream).
	StagingWrite(session string, r io.Reader) (digest string, size int64, err error)
	// StagingFinalize installs the pending object under digest. When the
	// caller's digest does not equal the digest computed at write time the
	// pending bytes are discarded and ErrContentAddressMismatch is returned.
	StagingFinalize(session, digest string) error
	// StagingAbort discards the pending object. Missing sessions are a no-op
	// (abort must be idempotent).
	StagingAbort(session string) error
}

func (s *Store) stagingDir() string { return filepath.Join(s.dir, "staging") }

func (s *Store) stagingPath(session string) string {
	return filepath.Join(s.stagingDir(), session+stagingPendingSuffix)
}

func (s *Store) stagingDigestPath(session string) string {
	return filepath.Join(s.stagingDir(), session+stagingDigestSuffix)
}

// StagingWrite streams bytes into a pending object while hashing them once.
// The digest is computed FROM THE STREAM — that is the point: the caller does
// not know the content address before uploading and must not re-read a
// multi-GB artifact to learn it.
func (s *Store) StagingWrite(session string, r io.Reader) (string, int64, error) {
	if !stagingSessionGrammar.MatchString(session) {
		return "", 0, fmt.Errorf("%w: %q", ErrStagingSession, session)
	}
	if err := os.MkdirAll(s.stagingDir(), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(s.stagingDir(), ".pending-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, digest), r)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, err
	}
	sum := hex.EncodeToString(digest.Sum(nil))
	if err := syncFile(tmpPath); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpPath, s.stagingPath(session)); err != nil {
		return "", 0, err
	}
	// The digest sidecar is what finalize compares against; it must be as
	// durable as the pending bytes, or a crash could pair stale bytes with a
	// forgotten digest.
	if err := os.WriteFile(s.stagingDigestPath(session), []byte(sum+"\n"), 0o644); err != nil {
		return "", 0, err
	}
	return sum, written, nil
}

// StagingFinalize installs the pending object under its content address.
// The caller's digest must equal the digest the store computed at write time:
// this is the same verify-before-install rule as PutReader, asserted at the
// only moment the caller can get it wrong (naming the address).
func (s *Store) StagingFinalize(session, digest string) error {
	if !stagingSessionGrammar.MatchString(session) {
		return fmt.Errorf("%w: %q", ErrStagingSession, session)
	}
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !CanonicalContentAddress(digest) {
		return fmt.Errorf("%w: %q (want %d lowercase hex characters)",
			ErrInvalidContentAddress, digest, SHA256HexLen)
	}
	recorded, err := os.ReadFile(s.stagingDigestPath(session))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: session %q", ErrStagingNotFound, session)
		}
		return err
	}
	got := strings.ToLower(strings.TrimSpace(string(recorded)))
	if got != digest {
		// The caller named a content address the bytes do not have: discard
		// the pending object rather than leaving a tempting wrong-keyed pair
		// behind (the same fail-closed posture as a rejected PUT).
		_ = s.StagingAbort(session)
		return fmt.Errorf("%w: session %s, asserted %s, content %s",
			ErrContentAddressMismatch, session, digest, got)
	}
	p := s.path(digest)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := syncFile(s.stagingPath(session)); err != nil {
		return err
	}
	if err := os.Rename(s.stagingPath(session), p); err != nil {
		return err
	}
	_ = os.Remove(s.stagingDigestPath(session))
	dir, err := os.Open(filepath.Dir(p))
	if err != nil {
		return fmt.Errorf("open object directory for sync: %w", err)
	}
	dirErr := dir.Sync()
	dir.Close()
	if dirErr != nil {
		return fmt.Errorf("sync object directory: %w", dirErr)
	}
	return nil
}

// StagingAbort discards the pending object; it is idempotent by contract.
func (s *Store) StagingAbort(session string) error {
	if !stagingSessionGrammar.MatchString(session) {
		return fmt.Errorf("%w: %q", ErrStagingSession, session)
	}
	err1 := os.Remove(s.stagingPath(session))
	err2 := os.Remove(s.stagingDigestPath(session))
	if err1 != nil && !os.IsNotExist(err1) {
		return err1
	}
	if err2 != nil && !os.IsNotExist(err2) {
		return err2
	}
	return nil
}
