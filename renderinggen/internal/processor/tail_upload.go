// tail_upload.go owns the streaming upload lane for fragmented overlay
// artifacts: the object-store upload starts when the RENDER starts and tails
// the output file while the mux appends to it, so the upload overlaps the
// encode instead of following it.
//
// The contract that makes it safe: a fragmented MP4's bytes are final while
// appended (no moov backpatch), so a reader that delivers every byte up to the
// producer's Finish signal delivers exactly the finished file. The digest is
// computed from the stream by BOTH the client and the store and asserted at
// finalize (storage.StoreStaged), so the content address the artifact is
// recorded under is proven against the bytes that were installed — the same
// `local_sha == objectstore_sha == db_sha` invariant as the classic
// hash-then-PUT path, without the pre-upload hash pass and without waiting for
// the file to complete before uploading it.
package processor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// stagedUpload is one in-flight (or completed) streaming artifact upload. The
// render lane drives it (start before the render, Finish/Abort at the commit
// point); the store phase consumes it (Wait) instead of uploading again.
type stagedUpload struct {
	reader *storage.GrowingFileReader
	done   chan struct{}

	mu        sync.Mutex
	committed bool // Finish was called: the stream is owned by the store phase
	sha       string
	size      int64
	err       error
	startedAt time.Time
	endAt     time.Time
}

// startTailUpload begins streaming outputPath into the store while the mux is
// still appending to it. The caller must Finish the upload at its commit point
// (the render can no longer fail) or Abort it on every error path.
func (p *Processor) startTailUpload(ctx context.Context, jobID, outputPath string) *stagedUpload {
	upload := &stagedUpload{
		reader:    storage.NewGrowingFileReader(outputPath),
		done:      make(chan struct{}),
		startedAt: time.Now(),
	}
	session := stagingSessionID(jobID)
	workerlog.ByJobID(jobID).Infof("tail upload started: session=%s output=%s", session, outputPath)
	go func() {
		defer close(upload.done)
		sha, size, err := p.store.PutStaged(ctx, session, upload.reader)
		upload.mu.Lock()
		upload.sha, upload.size, upload.err = sha, size, err
		upload.endAt = time.Now()
		upload.mu.Unlock()
		if err != nil {
			// Not fatal to the render here — the store phase decides. But a
			// failed streaming upload must never be invisible either.
			workerlog.ByJobID(jobID).Warnf("tail upload failed (session=%s): %v", session, err)
		}
	}()
	return upload
}

// Finish marks the output file complete (the mux is closed and no further
// append is possible). The upload drains the remainder and finalizes. From
// here the stream is owned by the store phase: a later Abort is a no-op, so
// the render lane's deferred cleanup cannot retroactively fail it.
func (u *stagedUpload) Finish() {
	u.mu.Lock()
	u.committed = true
	u.mu.Unlock()
	u.reader.Finish()
}

// Abort ends the upload without installing anything: the growing reader errors
// out, the streaming request fails and no finalize is asserted. It waits for
// the goroutine so no upload outlives the job that started it. No-op once
// Finish committed the stream — the store phase owns it from there.
func (u *stagedUpload) Abort() {
	u.mu.Lock()
	committed := u.committed
	u.mu.Unlock()
	if committed {
		return
	}
	u.reader.Abort(storage.ErrGrowingFileAborted)
	<-u.done
}

// Wait blocks until the streaming upload has completed and reports the digest
// computed from the stream, the streamed size and any failure. On success the
// bytes are already installed in the store under the returned digest.
func (u *stagedUpload) Wait() (string, int64, error) {
	<-u.done
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.sha, u.size, u.err
}

// Duration is the upload's own wall time — which ran CONCURRENTLY with the
// render, the overlap being the entire point of the lane.
func (u *stagedUpload) Duration() time.Duration {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.endAt.IsZero() {
		return time.Since(u.startedAt)
	}
	return u.endAt.Sub(u.startedAt)
}

// stagingSessionID derives a staging session id from a job id. The store's
// session grammar is `[A-Za-z0-9_-]{1,128}`, so anything else is replaced; a
// random suffix gives per-attempt uniqueness, so a retry can never race a
// stale session for the same job (the store's last-writer-wins replacement
// would otherwise corrupt an in-flight upload).
func stagingSessionID(jobID string) string {
	var b strings.Builder
	for _, r := range jobID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	id := b.String()
	if id == "" {
		id = "job"
	}
	var extra [8]byte
	_, _ = rand.Read(extra[:])
	id += "-" + hex.EncodeToString(extra[:])
	if len(id) > 128 {
		id = id[:128]
	}
	return id
}
