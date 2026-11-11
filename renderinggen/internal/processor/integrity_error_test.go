package processor

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// TestLocalContentAddressMismatchIsPermanent pins the classification of the
// mismatch detected at the worker boundary: when L3 or L2 returns bytes that do
// not hash to the requested address, retrying the same job repeats the same
// failure. It must be reported permanently, exactly like a store-refused write.
func TestLocalContentAddressMismatchIsPermanent(t *testing.T) {
	err := fmt.Errorf("processor: materialize asset: %w: key %s, content %s",
		storage.ErrContentAddressMismatch, "aaaa", "bbbb")
	if !isPermanentFailure(err) {
		t.Fatal("a local content-address mismatch must be a permanent failure")
	}
	if errors.Is(err, storage.ErrContentAddressRejected) {
		t.Fatal("fixture must exercise the local mismatch sentinel, not the store-rejection one")
	}
}
