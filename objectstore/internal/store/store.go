// Package store implements a disk-backed content-addressed object store.
//
// The store's one invariant is the content-addressing contract:
//
//	sha256(bytes stored at key) == key
//
// It is enforced on EVERY write, not on a special "verified" path. The HTTP
// object store previously accepted whatever bytes arrived under whatever key
// the caller named, so `PUT /objects/<sha-of-image-A>` with the bytes of
// image B returned 201, and every later `HEAD <sha-of-image-A>` answered 200
// to a worker that then trusted those bytes as A's. A content-addressed store
// that does not recompute the digest is not content-addressed; it is a
// key/value store with a misleading naming convention, and the mismatch only
// surfaces much later as an unexplained "expected SHA != downloaded bytes".
//
// Consequences of the invariant, all deliberately fail-closed:
//
//   - a key must be a CANONICAL content address (64 lowercase hex chars), so
//     one content can never be addressed by two spellings (uppercase vs
//     lowercase would otherwise create two directory entries for one digest);
//   - bytes are streamed to a temporary file while being hashed, so a
//     multi-GB artifact never has to be buffered to be verified;
//   - nothing is installed unless the digest matches, the length matches and
//     the fsyncs succeed — a rejected PUT leaves no object and no temp file.
//
// The write path cannot vouch for bytes that arrived any other way (a restored
// volume, a hand copy, bit rot), so the read-side half of the contract lives in
// integrity.go: Verify re-hashes one stored object and Scan audits the whole
// tree. Nothing is ever silently repaired — a violation is reported with the
// digest that was actually found.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("object not found")

// ErrInvalidContentAddress is returned when a key is not a canonical content
// address (64 lowercase hex characters). It is a caller error, not a storage
// failure, and the HTTP surface maps it to 400.
var ErrInvalidContentAddress = errors.New("invalid content address")

// ErrContentAddressMismatch is returned when the bytes offered for a key do
// not hash to that key. The object is NOT installed. It is a caller error (the
// producer is publishing the wrong bytes, or naming them wrongly) and the HTTP
// surface maps it to 422.
var ErrContentAddressMismatch = errors.New("content does not match its content address")

// SHA256HexLen is the length of a canonical SHA-256 content address: the full
// digest, hex encoded, lowercase.
const SHA256HexLen = 64

// ContentAddressProtocol is the versioned identity of the addressing contract
// this store implements. It is published on /health so a probe (or a person)
// can tell a VERIFYING store from a pre-verification one without hashing
// anything: a client that requires write verification can fail closed on an
// unknown value instead of discovering the gap from a corrupted render. Bump it
// whenever the address grammar or a verification guarantee changes.
const ContentAddressProtocol = "sha256-v1"

// PathLayout is the human-readable on-disk layout of an object address,
// published next to ContentAddressProtocol so an operator reading /health can
// see WHERE the bytes are meant to live without reading the source.
const PathLayout = "objects/<2 hex>/<64 hex>"

// Capabilities describes what this store promises, as machine-readable facts
// rather than prose. It is what a canary asserts before it trusts a store.
type Capabilities struct {
	// ContentAddress is the protocol version (ContentAddressProtocol).
	ContentAddress string `json:"content_address"`
	// AddressLength is the number of hex characters in an address.
	AddressLength int `json:"address_length"`
	// PathLayout is the on-disk layout of an address.
	PathLayout string `json:"path_layout"`
	// VerifyOnWrite is true because the write path recomputes the digest and
	// refuses to install bytes that disagree with their address. A store
	// reporting false here is a pre-verification build.
	VerifyOnWrite bool `json:"verify_on_write"`
	// VerifyOnRead is true because GET /objects/{key}/verify re-hashes a stored
	// object on demand (and Scan audits a whole tree). It does NOT mean every
	// read hashes the bytes: GET/HEAD stay cheap existence probes by design.
	VerifyOnRead bool `json:"verify_on_read"`
}

// Capabilities returns the addressing and verification guarantees of this
// store. It is a method rather than a package-level function because a store's
// guarantees belong to the instance a client is actually talking to: a future
// read-only replica, or a store constructed with verification disabled by
// policy, must describe itself rather than publishing a package constant.
func (s *Store) Capabilities() Capabilities {
	return Capabilities{
		ContentAddress: ContentAddressProtocol,
		AddressLength:  SHA256HexLen,
		PathLayout:     PathLayout,
		VerifyOnWrite:  true,
		VerifyOnRead:   true,
	}
}

// Store is a disk-backed, content-addressed object store.
type Store struct {
	dir string
}

// New creates a store rooted at dir.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// CanonicalContentAddress reports whether key is a canonical content address:
// exactly 64 lowercase hex characters. Uppercase hex and prefixed/suffixed
// spellings are rejected rather than normalised, so that one digest has
// exactly one on-disk address.
func CanonicalContentAddress(key string) bool {
	if len(key) != SHA256HexLen {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Put writes object data for key. The data must hash to key; see PutReader.
func (s *Store) Put(key string, data []byte) error {
	return s.PutReader(key, bytes.NewReader(data), int64(len(data)))
}

// PutReader streams an object into a temporary file, verifies that its SHA-256
// equals key, syncs it to stable storage and only then atomically installs it.
//
// There is deliberately no unverified write path: a caller cannot opt out of
// the content-address check, because a caller that opts out is exactly the
// hole this store exists to close. Use the empty reader for a zero-byte object
// if that is what you have — its digest is still checked.
//
// The HTTP 201 acknowledgement is only sent after the rename, and the rename is
// only made after the file contents (and the directory entry) are durable:
// without the fsyncs a power loss right after an acknowledged PUT could
// silently lose an artifact the worker already treated as persisted in L3.
//
// Returns ErrInvalidContentAddress when key is not canonical, and
// ErrContentAddressMismatch when the bytes do not hash to key. In both cases
// the temporary file is removed and nothing is visible under key.
func (s *Store) PutReader(key string, r io.Reader, size int64) error {
	if !CanonicalContentAddress(key) {
		return fmt.Errorf("%w: %q (want %d lowercase hex characters)", ErrInvalidContentAddress, key, SHA256HexLen)
	}
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".object-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Runs on every exit path, including the mismatch path below: a rejected
	// upload must not leave bytes behind under any name.
	defer os.Remove(tmpPath)

	// Hash while streaming. io.MultiWriter keeps the verification pass free of
	// a second read of a multi-GB artifact.
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, digest), r)
	if err == nil && size >= 0 && written != size {
		err = errors.New("content length mismatch")
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	// Verify BEFORE any durable install: the whole point is that the bytes and
	// the address agree before the object becomes visible.
	if got := hex.EncodeToString(digest.Sum(nil)); got != key {
		return fmt.Errorf("%w: key %s, content %s", ErrContentAddressMismatch, key, got)
	}

	if err := syncFile(tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return err
	}
	// Persist the rename itself so the acknowledged object survives a crash.
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

// syncFile flushes a file's contents to stable storage before it is exposed
// under its content address.
func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// Get reads object data for key.
//
// Deprecated: test-only byte path. Production uses Open (streaming) and
// LocalPath — buffering a multi-GB artifact into a byte slice would spike
// worker memory. Kept exported only for store_test (D3); do not use in prod.
func (s *Store) Get(key string) ([]byte, error) {
	if !CanonicalContentAddress(key) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidContentAddress, key)
	}
	data, err := os.ReadFile(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Open returns a streaming reader for the object plus its size (-1 when
// unknown). Callers must Close the reader. Used by the HTTP server so large
// artifacts are streamed from disk instead of buffered in RAM.
//
// Open does not re-hash the object: it is a cheap existence/streaming probe,
// and re-reading a multi-GB artifact on every GET/HEAD would defeat the
// streaming contract. Trust in the bytes comes from the write path, which
// refuses to install anything that does not hash to its address.
func (s *Store) Open(key string) (*os.File, int64, error) {
	if !CanonicalContentAddress(key) {
		return nil, 0, fmt.Errorf("%w: %q", ErrInvalidContentAddress, key)
	}
	p := s.path(key)
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (s *Store) path(key string) string {
	prefix := key
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(s.dir, "objects", prefix, key)
}
