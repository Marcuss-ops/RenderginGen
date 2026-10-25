// memory_staging.go — StagingBackend for the in-process Memory store.
//
// It mirrors the disk adapter's staging semantics exactly (last writer wins
// per session, finalize verifies the asserted digest before the object
// becomes visible, abort is idempotent) so the HTTP staging routes answer the
// same statuses on both backends — the seam contract of the server tests.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

type memoryPending struct {
	data   []byte
	digest string
}

// StagingWrite implements StagingBackend: one pass hashes while buffering.
func (m *Memory) StagingWrite(session string, r io.Reader) (string, int64, error) {
	if !stagingSessionGrammar.MatchString(session) {
		return "", 0, fmt.Errorf("%w: %q", ErrStagingSession, session)
	}
	digest := sha256.New()
	var buf bytes.Buffer
	written, err := io.Copy(io.MultiWriter(&buf, digest), r)
	if err != nil {
		return "", 0, err
	}
	sum := hex.EncodeToString(digest.Sum(nil))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = make(map[string]memoryPending)
	}
	m.pending[session] = memoryPending{data: buf.Bytes(), digest: sum}
	return sum, written, nil
}

// StagingFinalize implements StagingBackend: the asserted digest must equal
// the digest computed at write time before the object becomes addressable.
func (m *Memory) StagingFinalize(session, digest string) error {
	if !stagingSessionGrammar.MatchString(session) {
		return fmt.Errorf("%w: %q", ErrStagingSession, session)
	}
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !CanonicalContentAddress(digest) {
		return fmt.Errorf("%w: %q (want %d lowercase hex characters)",
			ErrInvalidContentAddress, digest, SHA256HexLen)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pending, ok := m.pending[session]
	if !ok {
		return fmt.Errorf("%w: session %q", ErrStagingNotFound, session)
	}
	if pending.digest != digest {
		delete(m.pending, session)
		return fmt.Errorf("%w: session %s, asserted %s, content %s",
			ErrContentAddressMismatch, session, digest, pending.digest)
	}
	m.objects[digest] = memoryObject{data: pending.data, modTime: time.Now()}
	delete(m.pending, session)
	return nil
}

// StagingAbort implements StagingBackend and is idempotent by contract.
func (m *Memory) StagingAbort(session string) error {
	if !stagingSessionGrammar.MatchString(session) {
		return fmt.Errorf("%w: %q", ErrStagingSession, session)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, session)
	return nil
}
