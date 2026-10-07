// memory.go is the second shipped Backend: an in-process store.
//
// It exists so the seam in backend.go is PROVED rather than asserted. The
// server tests run the full HTTP contract (verified writes, cheap HEAD,
// explicit /verify, byte ranges) over this implementation, which means a
// regression that reaches for the disk adapter again fails a test instead of
// being discovered when someone tries to put a shared backend behind the load
// balancer. It also gives an operator a way to smoke-test the HTTP surface
// without provisioning a data directory.
//
// It is deliberately NOT a production backend: the objects are process state
// and disappear on restart, and every byte lives in RAM.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// Memory is an in-process Backend. As with the disk adapter it enforces the
// content-addressing contract on every write, so the two implementations
// answer the same HTTP statuses for the same inputs.
type Memory struct {
	mu      sync.RWMutex
	objects map[string]memoryObject
}

type memoryObject struct {
	data    []byte
	modTime time.Time
}

// NewMemory creates an empty in-process store.
func NewMemory() *Memory {
	return &Memory{objects: make(map[string]memoryObject)}
}

// Capabilities implements Backend.
func (m *Memory) Capabilities() Capabilities { return capabilities() }

// Put implements Backend.
func (m *Memory) Put(key string, data []byte) error {
	return m.PutReader(key, bytes.NewReader(data), int64(len(data)))
}

// PutReader implements Backend. It hashes WHILE streaming into the buffer, so
// the bytes are read once: the digest is a by-product of the copy rather than a
// second pass, exactly like the disk adapter's io.MultiWriter.
func (m *Memory) PutReader(key string, r io.Reader, size int64) error {
	if !CanonicalContentAddress(key) {
		return fmt.Errorf("%w: %q (want %d lowercase hex characters)", ErrInvalidContentAddress, key, SHA256HexLen)
	}
	digest := sha256.New()
	var buf bytes.Buffer
	written, err := io.Copy(io.MultiWriter(&buf, digest), r)
	if err != nil {
		return err
	}
	if size >= 0 && written != size {
		return errors.New("content length mismatch")
	}
	// Verify BEFORE the object becomes visible, so a rejected write leaves the
	// address empty exactly as it does on disk.
	if got := hex.EncodeToString(digest.Sum(nil)); got != key {
		return fmt.Errorf("%w: key %s, content %s", ErrContentAddressMismatch, key, got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memoryObject{data: buf.Bytes(), modTime: time.Now()}
	return nil
}

// Get implements Backend. The bytes are copied out under the read lock so a
// concurrent overwrite cannot change them underneath an in-flight download.
func (m *Memory) Get(key string) (io.ReadSeekCloser, int64, time.Time, error) {
	if !CanonicalContentAddress(key) {
		return nil, 0, time.Time{}, fmt.Errorf("%w: %q", ErrInvalidContentAddress, key)
	}
	m.mu.RLock()
	object, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return nil, 0, time.Time{}, ErrNotFound
	}
	data := append([]byte(nil), object.data...)
	return nopReadSeekCloser{bytes.NewReader(data)}, int64(len(data)), object.modTime, nil
}

// Verify implements Backend with the same reporting shape as the disk adapter.
func (m *Memory) Verify(key string) (Integrity, error) {
	if !CanonicalContentAddress(key) {
		return Integrity{Key: key}, fmt.Errorf("%w: %q (want %d lowercase hex characters)", ErrInvalidContentAddress, key, SHA256HexLen)
	}
	m.mu.RLock()
	object, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return Integrity{Key: key}, ErrNotFound
	}
	sum := sha256.Sum256(object.data)
	got := hex.EncodeToString(sum[:])
	result := Integrity{Key: key, SHA256: got, SizeBytes: int64(len(object.data))}
	if got != key {
		result.Reason = fmt.Sprintf("stored bytes hash to %s, not to their address %s", got, key)
		return result, fmt.Errorf("%w: %s", ErrIntegrityViolation, result.Reason)
	}
	result.OK = true
	return result, nil
}

// Scan implements Backend. A write path that refuses non-canonical keys means
// every stored entry is addressable by construction, so the only shape this
// scan can find is a digest mismatch — reported, never repaired.
func (m *Memory) Scan() (IntegrityReport, error) {
	m.mu.RLock()
	keys := make([]string, 0, len(m.objects))
	for key := range m.objects {
		keys = append(keys, key)
	}
	m.mu.RUnlock()
	sort.Strings(keys)

	var report IntegrityReport
	for _, key := range keys {
		report.Objects++
		verified, err := m.Verify(key)
		switch {
		case err == nil:
			report.Verified++
		case errors.Is(err, ErrNotFound):
			// Raced with a delete; not a corruption, same as the disk scan.
		case errors.Is(err, ErrIntegrityViolation):
			verified.OK = false
			report.Failures = append(report.Failures, verified)
		default:
			return report, err
		}
	}
	return report, nil
}

// nopReadSeekCloser adapts an in-memory reader to the seekable streaming
// contract the HTTP surface needs (http.ServeContent requires io.ReadSeeker;
// there is nothing to release for bytes already in memory).
type nopReadSeekCloser struct{ *bytes.Reader }

func (nopReadSeekCloser) Close() error { return nil }

var _ Backend = (*Memory)(nil)
