// gc.go owns orphan collection for the content-addressed store.
//
// The store is a CAS: an object is identified by its bytes, not by the job
// that produced it. Publication writes the bytes first (streaming) and the
// queue ledger row last, so a crash or a rejected job can leave an address
// that no row references. Those bytes are not corruption — they are simply
// unreachable — but nothing ever removed them, so the volume grew with every
// failed publication.
//
// Collection is reference-driven and conservative:
//
//   - the caller supplies the REFERENCED addresses (the queue exports them from
//     the ledger; see queue/cmd/artifact-keys);
//   - an address in that set is never deleted, whatever its age;
//   - an unreferenced object younger than the retention window is reported but
//     kept, because a publication can still be in flight;
//   - the default is a DRY RUN: deletion is an explicit second step.
//
// The store deliberately does not invent a retention policy. The operator owns
// how long an object must be unreferenced before it is garbage.
package store

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GCOptions configures one collection pass.
type GCOptions struct {
	// Referenced is the set of content addresses the ledger still owns. It is
	// required: collecting with an empty set would delete every object, which
	// is a catastrophic typo, not a policy.
	Referenced map[string]struct{}
	// Delete actually removes unreferenced objects. False is the dry run.
	Delete bool
	// MinAge protects recently written objects from deletion. An object is
	// eligible only when it has been unreferenced-looking for at least this
	// long. Zero makes every unreferenced object eligible.
	MinAge time.Duration
	// Now is the reference clock; zero means time.Now().
	Now time.Time
}

// GCObject describes one collected or reported object.
type GCObject struct {
	Key       string
	Path      string
	SizeBytes int64
	ModTime   time.Time
	Reason    string
}

// GCReport is the outcome of a collection pass. Deleted is zero in a dry run.
type GCReport struct {
	Scanned     int
	Referenced  int
	KeptYoung   int
	Orphans     []GCObject
	OrphanBytes int64
	Deleted     int
	DeletedSize int64
	// Staging lists leftover ".object-*" upload files. They are interrupted
	// writes, never addressable objects, and follow the same age rule.
	Staging []GCObject
}

// ErrEmptyReferenceSet refuses a collection pass with no references. Deleting
// every object because the export file was empty is not a recoverable mistake.
var ErrEmptyReferenceSet = errors.New("objectstore: refusing to collect garbage with an empty reference set")

// CollectGarbage reports (and, with Delete, removes) every object that the
// referenced set does not own. The walk is deliberately shaped like Scan: it
// never trusts a filename to be a canonical address, so a malformed or
// misplaced file is collected instead of being silently ignored.
func (s *Store) CollectGarbage(opts GCOptions) (GCReport, error) {
	if len(opts.Referenced) == 0 {
		return GCReport{}, ErrEmptyReferenceSet
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-opts.MinAge)
	var report GCReport

	root := filepath.Join(s.dir, "objects")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		name := d.Name()
		if strings.HasPrefix(name, stagingPrefix) {
			if opts.MinAge > 0 && info.ModTime().After(cutoff) {
				report.KeptYoung++
				return nil
			}
			report.Staging = append(report.Staging, GCObject{
				Key: name, Path: path, SizeBytes: info.Size(), ModTime: info.ModTime(),
				Reason: "interrupted upload staging file",
			})
			if opts.Delete {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("remove staging file %s: %w", path, err)
				}
				report.Deleted++
				report.DeletedSize += info.Size()
			}
			return nil
		}

		report.Scanned++
		if _, ok := opts.Referenced[name]; ok && CanonicalContentAddress(name) && filepath.Base(filepath.Dir(path)) == name[:2] {
			report.Referenced++
			return nil
		}
		if opts.MinAge > 0 && info.ModTime().After(cutoff) {
			report.KeptYoung++
			return nil
		}
		reason := "no ledger row references this address"
		switch {
		case !CanonicalContentAddress(name):
			reason = "filename is not a canonical content address"
		case filepath.Base(filepath.Dir(path)) != name[:2]:
			reason = fmt.Sprintf("object is stored under shard %q, but its address says %q", filepath.Base(filepath.Dir(path)), name[:2])
		}
		report.Orphans = append(report.Orphans, GCObject{
			Key: name, Path: path, SizeBytes: info.Size(), ModTime: info.ModTime(), Reason: reason,
		})
		report.OrphanBytes += info.Size()
		if opts.Delete {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove orphan %s: %w", path, err)
			}
			report.Deleted++
			report.DeletedSize += info.Size()
		}
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

// LoadReferencedKeys reads one content address per line. Blank lines and lines
// starting with '#' are ignored, so the queue export can carry a header. A
// non-canonical entry is an error: silently dropping it would turn a typo into
// a deletion of the exact object the operator meant to protect.
func LoadReferencedKeys(path string) (map[string]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	keys := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if !CanonicalContentAddress(text) {
			return nil, fmt.Errorf("%s:%d: %q is not a canonical sha256 content address", path, line, text)
		}
		keys[text] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}
