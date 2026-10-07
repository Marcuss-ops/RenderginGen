// backend.go owns the seam between the HTTP surface and the storage engine.
//
// The disk-backed Store is the SINGLE-NODE adapter: it is right for dev, for
// tests and for a one-host deployment. It is not sufficient for a deployment
// with more than one object-store replica, because two replicas behind a load
// balancer each hold their own local disk: they contain DIFFERENT object sets,
// and a PUT that lands on replica A is a 404 to a GET that lands on replica B.
// Making the bytes live somewhere every replica can reach (a shared
// content-addressed volume, or an S3-compatible service) is therefore a
// deployment change — and this interface is what keeps it from being a
// rewrite.
//
// The content-addressing contract belongs to the contract, not to the disk
// adapter. An implementation that installs whatever bytes arrive under
// whatever key the caller named is NOT an implementation of this interface:
// the HTTP surface promises 422 for bytes that disagree with their address,
// and /health promises verify_on_write, so a backend that cannot recompute the
// digest cannot honour either. Same for the read side: Get stays a cheap
// existence probe, while Verify and Scan are the explicit ways to ask whether
// the stored bytes still match their address.
package store

import (
	"io"
	"time"
)

// Backend is the storage contract the HTTP surface is written against.
//
// Implementations must be safe for concurrent use: net/http serves requests
// concurrently, and one backend instance serves all of them.
type Backend interface {
	// Capabilities describes the addressing and verification guarantees of
	// the implementation. It is published on /health so a probe can tell a
	// verifying store from a pre-verification one without hashing anything.
	Capabilities() Capabilities

	// Put stores data under key, which must be the lowercase SHA-256 hex
	// digest of data. It returns ErrInvalidContentAddress or
	// ErrContentAddressMismatch, and installs nothing, on failure.
	Put(key string, data []byte) error

	// PutReader is the streaming form of Put. Implementations must verify the
	// digest while streaming, so a multi-GB artifact is never buffered twice
	// (once to hash it and again to store it).
	PutReader(key string, r io.Reader, size int64) error

	// Get returns a seekable reader for the object, its size (-1 when
	// unknown) and its last modification time. Key is deliberately NOT
	// re-hashed here: GET/HEAD are cheap existence probes, and re-reading a
	// multi-GB artifact on every read would defeat the streaming contract.
	// It returns ErrNotFound for an absent object and
	// ErrInvalidContentAddress for a malformed key.
	Get(key string) (io.ReadSeekCloser, int64, time.Time, error)

	// Verify recomputes the SHA-256 of the bytes stored at key and reports the
	// facts actually measured, so a violation is diagnosable without a second
	// read.
	Verify(key string) (Integrity, error)

	// Scan audits the whole store and reports every object whose bytes
	// contradict their address plus any leftover staging file.
	Scan() (IntegrityReport, error)
}

// capabilities is the single definition of what the shipped backends promise:
// the address grammar and the two verification guarantees. It is a function
// rather than a package variable so callers cannot mutate the published
// contract, and it is shared by every backend so a new implementation cannot
// drift from the /health payload a canary asserts.
func capabilities() Capabilities {
	return Capabilities{
		ContentAddress: ContentAddressProtocol,
		AddressLength:  SHA256HexLen,
		PathLayout:     PathLayout,
		VerifyOnWrite:  true,
		VerifyOnRead:   true,
	}
}

// The disk adapter must implement the seam it is the reference for: a compile
// error here is easier to read than a 500 at request time.
var _ Backend = (*Store)(nil)
