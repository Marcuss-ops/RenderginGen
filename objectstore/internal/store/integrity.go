// integrity.go owns the READ-side half of the content-addressing contract.
//
// The write path (store.go) proves that bytes hash to their address before it
// installs them. That guarantee covers every object this process accepted, and
// nothing else. It says nothing about an object that arrived any other way:
//
//   - a store directory restored from a backup or synced from another host;
//   - a hand-copied file, or a botched migration between disks;
//   - bit rot / a truncated file on the underlying filesystem;
//   - a crash between the rename and the directory fsync on a filesystem that
//     did not honour it.
//
// In all of those cases Open/HEAD still answers 200, because they are cheap
// existence probes by design (re-reading a multi-GB artifact to answer "is it
// there?" would defeat streaming). The bytes would be served as if they were
// the address they are stored under, which is the same failure the verified
// write path exists to prevent — just reached through the filesystem instead of
// through the API.
//
// Verify and Scan close that gap. They are deliberately read-only: an integrity
// check that silently repairs what it finds cannot be used as evidence, and a
// store that rewrites bytes while an operator is auditing it is a worse
// problem than the one being audited. A violation is REPORTED, with the digest
// that was actually found, so an operator can decide between re-uploading the
// object from its producer and restoring the store from backup.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrIntegrityViolation is returned when the bytes stored at a content address
// do not hash to that address. Unlike ErrContentAddressMismatch (a write was
// refused), this means the store is ALREADY holding bytes that contradict
// their address: the object must be re-published or restored, and any reader
// that trusted it may have used the wrong bytes.
var ErrIntegrityViolation = errors.New("stored bytes do not match their content address")

// stagingPrefix is the name prefix os.CreateTemp uses for in-flight uploads.
// A leftover file with this prefix is a crashed or killed write, never an
// object: it is not a canonical content address, so no reader can address it.
const stagingPrefix = ".object-"

// Integrity is the outcome of verifying one object. SHA256 and SizeBytes are
// always the facts measured from the stored bytes — never the claim — so a
// violation is diagnosable without re-reading the file. Reason is empty when
// OK is true.
type Integrity struct {
	Key       string
	Path      string
	SHA256    string
	SizeBytes int64
	OK        bool
	Reason    string
}

// IntegrityReport summarises a full scan of the store's object tree.
type IntegrityReport struct {
	// Objects is the number of candidate object files examined, excluding
	// leftover staging files.
	Objects int
	// Verified is how many of them hashed to the address they are stored
	// under.
	Verified int
	// Failures is every object that did not, in walk order.
	Failures []Integrity
	// Staging lists leftover ".object-*" files from interrupted writes. They
	// are reported, not deleted: removing them is a mutating operation.
	Staging []string
}

// Clean reports whether the scan found nothing wrong.
func (r IntegrityReport) Clean() bool {
	return len(r.Failures) == 0 && len(r.Staging) == 0
}

// Verify recomputes the SHA-256 of the bytes stored at key and compares it with
// key itself.
//
// On a match it returns Integrity{OK: true}. On a mismatch it returns the
// measured facts with OK false AND ErrIntegrityViolation, so a caller can both
// branch on the error (errors.Is) and log the digest that was actually found.
// ErrInvalidContentAddress and ErrNotFound are returned unchanged for a
// malformed key and an absent object.
func (s *Store) Verify(key string) (Integrity, error) {
	if !CanonicalContentAddress(key) {
		return Integrity{Key: key}, fmt.Errorf("%w: %q (want %d lowercase hex characters)", ErrInvalidContentAddress, key, SHA256HexLen)
	}
	p := s.path(key)
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return Integrity{Key: key, Path: p}, ErrNotFound
	}
	if err != nil {
		return Integrity{Key: key, Path: p}, err
	}
	defer f.Close()

	digest := sha256.New()
	size, err := io.Copy(digest, f)
	if err != nil {
		return Integrity{Key: key, Path: p}, err
	}
	got := hex.EncodeToString(digest.Sum(nil))
	result := Integrity{Key: key, Path: p, SHA256: got, SizeBytes: size}
	if got != key {
		result.Reason = fmt.Sprintf("stored bytes hash to %s, not to their address %s", got, key)
		return result, fmt.Errorf("%w: %s", ErrIntegrityViolation, result.Reason)
	}
	result.OK = true
	return result, nil
}

// Scan walks the store's object tree and verifies every object in it. It is the
// whole-store counterpart to Verify: the operation an operator runs after a
// volume restore, a disk migration, or a suspicious incident, and the operation
// a canary runs to prove the store it is about to trust actually holds the
// bytes its addresses claim.
//
// Three shapes are reported as failures, because each one means an address
// cannot be trusted:
//
//	digest mismatch        the bytes contradict their address;
//	non-canonical name     the file can never be addressed by a reader;
//	wrong shard directory  the file is not where its own address says it is.
//
// A missing object tree is NOT an error: an empty store is a valid store, and a
// scan of a fresh volume must report "nothing to check" rather than fail.
func (s *Store) Scan() (IntegrityReport, error) {
	var report IntegrityReport
	root := filepath.Join(s.dir, "objects")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, stagingPrefix) {
			report.Staging = append(report.Staging, path)
			return nil
		}
		report.Objects++
		entry := Integrity{Key: name, Path: path}
		switch {
		case !CanonicalContentAddress(name):
			entry.Reason = "filename is not a canonical content address"
		case filepath.Base(filepath.Dir(path)) != name[:2]:
			entry.Reason = fmt.Sprintf("object is stored under shard %q, but its address says %q", filepath.Base(filepath.Dir(path)), name[:2])
		default:
			verified, err := s.Verify(name)
			if err == nil {
				report.Verified++
				return nil
			}
			if errors.Is(err, ErrNotFound) {
				// Raced with a delete between the walk and the open; not a
				// corruption.
				return nil
			}
			if !errors.Is(err, ErrIntegrityViolation) {
				return err
			}
			entry.SHA256, entry.SizeBytes, entry.OK, entry.Reason = verified.SHA256, verified.SizeBytes, false, verified.Reason
		}
		report.Failures = append(report.Failures, entry)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	return report, nil
}
