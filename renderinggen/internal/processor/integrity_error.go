package processor

import (
	"errors"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// ErrAssetContentMismatch marks a deterministic producer-contract violation:
// the bytes downloaded for an asset do not hash to the content address the job
// promised. Retrying the same job re-downloads the same wrong bytes, so it must
// be reported with FailPermanently instead of consuming the queue's retry
// budget.
//
// It is the RenderingGen half of the contract PipelineGen types in
// internal/kernel/asset.ErrContentAddressMismatch: the submitter detects the
// 422 while staging an asset, the worker detects the same mismatch while
// self-healing or storing it.
var ErrAssetContentMismatch = errors.New("processor: asset bytes do not hash to the promised content address")

// isPermanentFailure reports whether err is deterministic for this job and
// therefore must not re-enter the retryable Fail/Rendered states.
//
// Two families qualify:
//   - a required capability that this worker does not have
//     (ErrDrivePublicationPermanent);
//   - an asset whose bytes the object store PROVED do not hash to the address
//     they were sent under (storage.ErrContentAddressRejected, HTTP 400/422).
//
// Everything else — a transport failure, a missing local artifact, a 5xx — is
// left to the normal retry policy.
func isPermanentFailure(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrDrivePublicationPermanent) ||
		errors.Is(err, ErrAssetContentMismatch) ||
		errors.Is(err, storage.ErrContentAddressRejected)
}
