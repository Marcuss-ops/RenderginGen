package store

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestMemoryBackendHonoursTheContentAddressContract pins that the second
// backend answers exactly what the disk adapter answers for the same inputs.
// Two implementations of one contract are only interchangeable if the contract
// is what they are tested against, not the storage medium.
func TestMemoryBackendHonoursTheContentAddressContract(t *testing.T) {
	m := NewMemory()
	payload := []byte("shared backend payload")
	key := digestOf(payload)

	if err := m.Put(key, payload); err != nil {
		t.Fatalf("put: %v", err)
	}

	reader, size, _, err := m.Get(key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer reader.Close()
	if size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body = %q, want %q", body, payload)
	}

	integrity, err := m.Verify(key)
	if err != nil || !integrity.OK || integrity.SHA256 != key {
		t.Fatalf("verify = %+v, err = %v", integrity, err)
	}

	report, err := m.Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Objects != 1 || report.Verified != 1 || !report.Clean() {
		t.Fatalf("scan = %+v, want 1 object verified and clean", report)
	}
}

// TestMemoryBackendRejectsBytesThatDoNotMatchTheAddress is the write-path half.
// A backend that accepted these bytes would be indistinguishable from the
// pre-verification store the disk adapter was written to replace, so the
// mismatch must be refused here too — the check lives in the contract, not in
// the filesystem.
func TestMemoryBackendRejectsBytesThatDoNotMatchTheAddress(t *testing.T) {
	m := NewMemory()
	key := digestOf([]byte("the bytes that belong at this address"))

	if err := m.Put(key, []byte("a different payload")); !errors.Is(err, ErrContentAddressMismatch) {
		t.Fatalf("mismatched put: err = %v, want ErrContentAddressMismatch", err)
	}
	if _, _, _, err := m.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("address must stay empty after a rejected put: err = %v", err)
	}

	for _, malformed := range []string{"abc", "plan.json", strings.ToUpper(key)} {
		if err := m.Put(malformed, []byte("x")); !errors.Is(err, ErrInvalidContentAddress) {
			t.Errorf("put %q: err = %v, want ErrInvalidContentAddress", malformed, err)
		}
		if _, _, _, err := m.Get(malformed); !errors.Is(err, ErrInvalidContentAddress) {
			t.Errorf("get %q: err = %v, want ErrInvalidContentAddress", malformed, err)
		}
		if _, err := m.Verify(malformed); !errors.Is(err, ErrInvalidContentAddress) {
			t.Errorf("verify %q: err = %v, want ErrInvalidContentAddress", malformed, err)
		}
	}
}

// TestMemoryBackendScanReportsBytesThatChangedBehindTheApi reproduces the
// read-side incident the read-side contract exists for: the bytes were replaced
// without going through Put (a restore, a hand copy, bit rot), so the write
// path's guarantee no longer covers them and the scan must say so.
func TestMemoryBackendScanReportsBytesThatChangedBehindTheApi(t *testing.T) {
	m := NewMemory()
	payload := []byte("the original bytes")
	key := digestOf(payload)
	if err := m.Put(key, payload); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Reach past the API the way a restore does: overwrite the stored bytes
	// while keeping the key that names them.
	m.mu.Lock()
	m.objects[key] = memoryObject{data: []byte("the TAMPERED byt"), modTime: m.objects[key].modTime}
	m.mu.Unlock()

	report, err := m.Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Clean() {
		t.Fatal("scan reported a clean store while an object contradicts its own address")
	}
	if len(report.Failures) != 1 || report.Failures[0].OK {
		t.Fatalf("failures = %+v", report.Failures)
	}
	if report.Failures[0].Key != key {
		t.Fatalf("failure key = %q, want %q", report.Failures[0].Key, key)
	}
}
