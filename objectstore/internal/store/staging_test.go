package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestStagingFinalizeRehashesPendingBytes pins the invariant finalize exists to
// enforce: sha256(bytes installed at key) == key. The digest sidecar records
// what the WRITER computed, but the sidecar and the pending file are two names;
// a crash between their updates, or a producer interacting with an older build,
// can leave a sidecar that does not describe the pending bytes. Finalize must
// hash the bytes it is about to install instead of trusting the sidecar.
func TestStagingFinalizeRehashesPendingBytes(t *testing.T) {
	s := New(t.TempDir())
	payload := []byte("trusted pending bytes")
	digest, size, err := s.StagingWrite("job-tamper", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("StagingWrite: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("StagingWrite size = %d, want %d", size, len(payload))
	}

	// Corrupt the pending bytes while leaving the digest sidecar untouched:
	// exactly the wrong-keyed pair a race (or a crash) could produce.
	if err := os.WriteFile(s.stagingPath("job-tamper"), []byte("tampered pending bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.StagingFinalize("job-tamper", digest); !errors.Is(err, ErrContentAddressMismatch) {
		t.Fatalf("StagingFinalize = %v, want ErrContentAddressMismatch", err)
	}
	if _, err := s.Verify(digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Verify after rejected finalize = %v, want ErrNotFound (nothing may be installed)", err)
	}
	if _, err := os.Stat(s.stagingPath("job-tamper")); !os.IsNotExist(err) {
		t.Fatalf("pending bytes survived a rejected finalize (stat err = %v)", err)
	}
	if _, err := os.Stat(s.stagingDigestPath("job-tamper")); !os.IsNotExist(err) {
		t.Fatalf("digest sidecar survived a rejected finalize (stat err = %v)", err)
	}
}

// TestStagingConcurrentWritesKeepBytesAndDigestPaired reproduces the race the
// per-session lock closes: several writers target one session at once. Whatever
// write wins, the pending file and the digest sidecar must describe the SAME
// stream, and finalize must install bytes that hash to the digest it returns.
// Before the fix, the .pending rename and the .sha256 write were independent,
// so the last writer's bytes could be paired with an earlier writer's digest.
func TestStagingConcurrentWritesKeepBytesAndDigestPaired(t *testing.T) {
	s := New(t.TempDir())
	const writers = 8
	for round := 0; round < 20; round++ {
		session := fmt.Sprintf("session-%d", round)
		payloads := make([][]byte, writers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			payloads[i] = bytes.Repeat([]byte{byte('a' + i)}, 4096)
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if _, _, err := s.StagingWrite(session, bytes.NewReader(payloads[i])); err != nil {
					t.Errorf("writer %d: %v", i, err)
				}
			}(i)
		}
		close(start)
		wg.Wait()

		sidecar, err := os.ReadFile(s.stagingDigestPath(session))
		if err != nil {
			t.Fatalf("round %d: read sidecar: %v", round, err)
		}
		recorded := strings.TrimSpace(string(sidecar))
		pending, err := os.ReadFile(s.stagingPath(session))
		if err != nil {
			t.Fatalf("round %d: read pending: %v", round, err)
		}
		if got := digestOf(pending); got != recorded {
			t.Fatalf("round %d: sidecar digest %s does not describe the pending bytes (%s)", round, recorded, got)
		}

		// The recorded digest belongs to exactly one writer's stream: identify
		// it, then prove finalize installed those bytes under that address.
		var winner []byte
		for _, payload := range payloads {
			if digestOf(payload) == recorded {
				winner = payload
				break
			}
		}
		if winner == nil {
			t.Fatalf("round %d: sidecar digest %s matches no writer payload", round, recorded)
		}
		if err := s.StagingFinalize(session, recorded); err != nil {
			t.Fatalf("round %d: finalize: %v", round, err)
		}
		installed, err := os.ReadFile(s.path(recorded))
		if err != nil {
			t.Fatalf("round %d: read installed object: %v", round, err)
		}
		if !bytes.Equal(installed, winner) {
			t.Fatalf("round %d: installed bytes are not the winner's stream", round)
		}
		if _, err := s.Verify(recorded); err != nil {
			t.Fatalf("round %d: installed object does not verify: %v", round, err)
		}
		// Finalize consumes the whole workspace: neither name may survive.
		for _, p := range []string{s.stagingPath(session), s.stagingDigestPath(session)} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("round %d: finalize left %s behind (stat err = %v)", round, p, err)
			}
		}
	}
}

// TestStagingFinalizeWhileWriteInFlight pins the session lock boundary: a
// writer hashes and stages its temp file without the session lock, while
// finalize commits an earlier attempt. Finalize must install the bytes that
// match its asserted digest; the in-flight writer must then commit a complete
// new pending/digest pair, never leave finalize with a half-replaced session.
func TestStagingFinalizeWhileWriteInFlight(t *testing.T) {
	s := New(t.TempDir())
	const session = "overlap-session"
	first := []byte("first attempt bytes")
	second := []byte("second attempt bytes")
	digestFirst, _, err := s.StagingWrite(session, bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}

	pr, pw := io.Pipe()
	writeDone := make(chan error, 1)
	go func() {
		_, _, err := s.StagingWrite(session, pr)
		writeDone <- err
	}()

	// The writer is blocked before it can read the second payload, so finalize
	// must commit the first attempt without waiting for it.
	if err := s.StagingFinalize(session, digestFirst); err != nil {
		t.Fatalf("finalize first attempt while writer is in flight: %v", err)
	}
	if _, err := pw.Write(second); err != nil {
		t.Fatal(err)
	}
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("second write: %v", err)
	}

	installed, err := os.ReadFile(s.path(digestFirst))
	if err != nil || !bytes.Equal(installed, first) {
		t.Fatalf("first attempt was not installed intact: %q, err=%v", installed, err)
	}
	digestSecond := digestOf(second)
	sidecar, err := os.ReadFile(s.stagingDigestPath(session))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(sidecar)); got != digestSecond {
		t.Fatalf("sidecar = %s, want second digest %s", got, digestSecond)
	}
	pending, err := os.ReadFile(s.stagingPath(session))
	if err != nil || !bytes.Equal(pending, second) {
		t.Fatalf("pending second attempt = %q, err=%v", pending, err)
	}
	if err := s.StagingFinalize(session, digestSecond); err != nil {
		t.Fatalf("finalize second attempt: %v", err)
	}
	if got, err := os.ReadFile(s.path(digestSecond)); err != nil || !bytes.Equal(got, second) {
		t.Fatalf("second attempt was not installed intact: %q, err=%v", got, err)
	}
}
