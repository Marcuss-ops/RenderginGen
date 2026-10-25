// staging_client.go — the worker side of the object store's streaming
// staging flow (PUT /objects/staging/{session} → POST .../finalize).
//
// It exists for producers whose bytes are final while they are appended: a
// fragmented MP4 overlay artifact can be streamed to the store WITHOUT a
// pre-upload hash pass over the completed file — the digest is computed FROM
// THE STREAM (one pass), asserted at finalize, and the store re-verifies it
// before the object becomes visible under its content address. The
// content-addressing contract is identical to StoreReader; only the moment
// the key becomes known differs.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrStagingUnsupported is returned by Client.PutStaged when the L3 backend
// has no streaming staging surface. Callers probe SupportsStaging first when
// they want the hash-then-PUT fallback instead of a hard failure.
var ErrStagingUnsupported = errors.New("storage backend does not support streaming staging")

// SupportsStaging reports whether the L3 backend implements StagedUploader —
// i.e. whether a producer may stream bytes whose address is not known yet.
func (c *Client) SupportsStaging() bool {
	_, ok := c.backend.(StagedUploader)
	return ok
}

// PutStaged streams r into the store through the staging flow and returns the
// content address computed FROM THE STREAM. It is the one-pass alternative to
// PutReader for producers whose bytes are final while they are appended: no
// pre-upload hash pass over the finished file is needed, and the upload can
// overlap the producing render (see the processor's tail-upload lane).
func (c *Client) PutStaged(ctx context.Context, session string, r io.Reader) (string, int64, error) {
	uploader, ok := c.backend.(StagedUploader)
	if !ok {
		return "", 0, ErrStagingUnsupported
	}
	return uploader.StoreStaged(ctx, session, r)
}

// StagedUploader is the optional streaming-staging extension of the writer
// surface. A backend that cannot stage keeps the hash-then-PUT contract.
type StagedUploader interface {
	// StoreStaged streams r into a pending store object and finalizes it
	// under the SHA-256 computed from the stream. It returns that digest and
	// the streamed size.
	StoreStaged(ctx context.Context, session string, r io.Reader) (sha256Hex string, size int64, err error)
}

// StoreStaged streams the body to the staging route while hashing it once,
// then asserts the computed digest at finalize. The store independently
// verifies the digest it computed server-side, so a corrupted stream can
// never install bytes under a wrong address: the finalize is refused with
// HTTP 422 and the pending bytes are discarded.
func (h *HTTP) StoreStaged(ctx context.Context, session string, r io.Reader) (string, int64, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return "", 0, fmt.Errorf("storage: staged upload requires a session id")
	}
	digest := sha256.New()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		h.base+"/objects/staging/"+session, io.TeeReader(r, digest))
	if err != nil {
		return "", 0, fmt.Errorf("storage: staged upload request: %w", err)
	}
	h.setAuth(req)
	// The size is deliberately unknown (the producer is still appending):
	// chunked transfer is the point of the staging flow.
	resp, err := h.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("storage: staged upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return "", 0, fmt.Errorf("storage: staged upload: HTTP %d: %s",
			resp.StatusCode, errorDetail(resp.Body))
	}
	var staged struct {
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&staged); err != nil {
		return "", 0, fmt.Errorf("storage: staged upload response: %w", err)
	}
	computed := hex.EncodeToString(digest.Sum(nil))
	// The store's computed digest and the client's must agree — a mismatch is
	// stream corruption in flight, and finalizing either value would name the
	// bytes wrongly. The pending object is discarded before returning: a
	// rejected stream must not leave its bytes staged under any name.
	if staged.SHA256 != computed {
		h.abortStaged(session)
		return "", 0, fmt.Errorf("storage: staged upload digest mismatch: client %s, store %s",
			computed, staged.SHA256)
	}
	finalize, _ := json.Marshal(map[string]string{"sha256": computed})
	req, err = http.NewRequestWithContext(ctx, http.MethodPost,
		h.base+"/objects/staging/"+session+"/finalize", strings.NewReader(string(finalize)))
	if err != nil {
		return "", 0, fmt.Errorf("storage: staged finalize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	h.setAuth(req)
	resp2, err := h.client.Do(req)
	if err != nil {
		h.abortStaged(session)
		return "", 0, fmt.Errorf("storage: staged finalize: %w", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusCreated {
		h.abortStaged(session)
		return "", 0, fmt.Errorf("storage: staged finalize: HTTP %d: %s",
			resp2.StatusCode, errorDetail(resp2.Body))
	}
	return computed, staged.SizeBytes, nil
}

// abortStaged discards a pending staging object after a failed upload or
// finalize. Best-effort by contract (DELETE is idempotent server-side): the
// pending workspace is private, so a stale pending object is unreferenced
// garbage rather than a wrong visible byte — the cleanup is hygiene, not
// correctness, and must never mask the error the caller is already carrying.
func (h *HTTP) abortStaged(session string) {
	req, err := http.NewRequest(http.MethodDelete, h.base+"/objects/staging/"+session, nil)
	if err != nil {
		return
	}
	h.setAuth(req)
	resp, err := h.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
