package processor

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestStagingSessionIDPreservesRandomSuffixForLongJobIDs pins the truncation
// order. The store's session grammar is 128 characters and ValidateJobID admits
// job ids up to 256. Truncating the concatenation erased the entire random tail
// for ids at or above 128 characters, so two retries of the same job produced
// the SAME staging session and could race each other's pending bytes. The
// suffix must be reserved first; only the job-derived prefix may be shortened.
func TestStagingSessionIDPreservesRandomSuffixForLongJobIDs(t *testing.T) {
	const suffixLen = 17 // "-" + 16 hex characters
	for _, n := range []int{111, 112, 128, 256} {
		jobID := strings.Repeat("j", n)
		first := stagingSessionID(jobID)
		second := stagingSessionID(jobID)

		for _, id := range []string{first, second} {
			if len(id) > 128 {
				t.Fatalf("job id %d: session length %d exceeds the store grammar", n, len(id))
			}
			if len(id) != 128 {
				t.Fatalf("job id %d: session length %d, want 128 (111-byte prefix + reserved suffix)", n, len(id))
			}
			suffix := id[len(id)-suffixLen:]
			if suffix[0] != '-' {
				t.Fatalf("job id %d: suffix %q does not start with '-'", n, suffix)
			}
			if _, err := hex.DecodeString(suffix[1:]); err != nil {
				t.Fatalf("job id %d: suffix %q is not 16 hex characters: %v", n, suffix, err)
			}
		}
		if first == second {
			t.Fatalf("job id %d: two attempts share session %q (random suffix was truncated away)", n, first)
		}
		if got := first[:111]; got != strings.Repeat("j", 111) {
			t.Fatalf("job id %d: prefix is not the leading job-id bytes", n)
		}
	}

	// Short ids are not padded or truncated before the suffix.
	id := stagingSessionID("job-1")
	if len(id) != len("job-1")+suffixLen {
		t.Fatalf("short session length = %d, want %d", len(id), len("job-1")+suffixLen)
	}
	if !strings.HasPrefix(id, "job-1-") {
		t.Fatalf("short session = %q, want it to start with job-1-", id)
	}
}
