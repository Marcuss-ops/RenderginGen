// growing_file.go — a reader for a file that is STILL BEING APPENDED.
//
// It exists for the tail-upload lane: a fragmented MP4 overlay artifact has
// bytes that are final while they are appended, so the object-store upload can
// start before the producing file is complete and simply TAIL it. The reader
// serves whatever exists now, waits while the producer appends more, and ends
// exactly at the producer's Finish signal — never before (a byte appended
// before Finish is always delivered) and never after (an EOF before Finish is
// never reported).
//
// The producer here is another process (the render/mux), so growth discovery
// is a poll: there is no in-process notification to wait on. The poll interval
// only trades wake-ups for upload latency; it cannot affect correctness.
package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// ErrGrowingFileMissing is returned when the producer signalled completion but
// the file never appeared. Uploading a zero-byte object under the empty-bytes
// digest would be a silent wrongness; a missing output is a failure to report.
var ErrGrowingFileMissing = errors.New("storage: growing file never appeared")

// ErrGrowingFileAborted is returned by Read after Abort: the stream is over and
// nothing more may be consumed from it.
var ErrGrowingFileAborted = errors.New("storage: growing file aborted")

// defaultGrowingPoll is the growth-discovery interval. It bounds only how
// quickly the tail notices a new append (worst case: one interval of extra
// upload latency), never what bytes are delivered.
const defaultGrowingPoll = 20 * time.Millisecond

// GrowingFileReader reads a file while another process appends to it. Reads
// block until bytes arrive or the producer calls Finish; see the package
// comment for the delivery contract.
type GrowingFileReader struct {
	path string
	poll time.Duration

	mu       sync.Mutex
	f        *os.File
	finished bool
	aborted  error
	done     chan struct{} // closed by Finish/Abort to wake a blocked Read
}

// NewGrowingFileReader starts tailing path. The file need not exist yet; it is
// opened (and re-opened) lazily while the reader is polled.
func NewGrowingFileReader(path string) *GrowingFileReader {
	return &GrowingFileReader{
		path: path,
		poll: defaultGrowingPoll,
		done: make(chan struct{}),
	}
}

// Finish tells the reader the producer will append no more bytes. Reads drain
// the remainder and then report io.EOF. Idempotent.
func (g *GrowingFileReader) Finish() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finished || g.aborted != nil {
		return
	}
	g.finished = true
	close(g.done)
}

// Abort ends the stream with an error: a blocked or subsequent Read returns
// err (ErrGrowingFileAborted when nil is passed). Idempotent, and a NO-OP once
// Finish has committed the stream — the caller's deferred cleanup must never
// retroactively fail a stream whose bytes were already delivered and installed.
func (g *GrowingFileReader) Abort(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.aborted != nil || g.finished {
		return
	}
	if err == nil {
		err = ErrGrowingFileAborted
	}
	g.aborted = err
	if !g.finished {
		g.finished = true
		close(g.done)
	}
}

// Read implements io.Reader over the growing file.
//
// The EOF race is closed by construction: once Finish is observed, one more
// read is attempted before reporting EOF. The producer contracts that every
// write completes before Finish, so that final read happens-after the
// observation and therefore sees every byte.
func (g *GrowingFileReader) Read(p []byte) (int, error) {
	for {
		g.mu.Lock()
		finished, aborted := g.finished, g.aborted
		g.mu.Unlock()
		if aborted != nil {
			return 0, aborted
		}
		if g.f == nil {
			f, err := os.Open(g.path)
			if err != nil {
				if finished {
					return 0, fmt.Errorf("%w: %s", ErrGrowingFileMissing, g.path)
				}
				g.wait()
				continue
			}
			g.f = f
		}
		n, err := g.f.Read(p)
		if n > 0 {
			return n, nil
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		if finished {
			// One more attempt: a write may have landed between the empty
			// read above and the producer's Finish.
			n, err = g.f.Read(p)
			if n > 0 {
				return n, nil
			}
			if err != nil && err != io.EOF {
				return 0, err
			}
			return 0, io.EOF
		}
		g.wait()
	}
}

// wait blocks for the poll interval or until Finish/Abort, whichever comes
// first.
func (g *GrowingFileReader) wait() {
	select {
	case <-g.done:
	case <-time.After(g.poll):
	}
}
