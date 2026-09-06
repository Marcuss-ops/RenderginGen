// Package hashio provides the single streaming "copy while hashing" primitive
// used across the worker: every place that reads a byte stream and must know
// its SHA-256 content address (L3 downloads staged into L2, artifact output
// verification, parent-assembly hashing) goes through Copy or Reader so the
// knowledge is expressed once.
//
// The primitive itself is allocation-free per chunk and constant-memory; it
// never buffers the stream. Size-capping is NOT built in: each call site owns
// its byte budget, because only network-fed streams need one (see the
// processor's maxAssetDownloadBytes at the L3 self-heal call site). Local-file
// and L3→L2 streams are trusted, so a cap there would only add a failure mode
// nobody can hit.
package hashio

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// Copy copies r into w while computing the SHA-256 of the copied bytes,
// returning the hex digest and the total byte count (constant memory).
func Copy(r io.Reader, w io.Writer) (digest string, size int64, err error) {
	hasher := sha256.New()
	size, err = io.Copy(io.MultiWriter(w, hasher), r)
	if err != nil {
		return "", size, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// Reader computes the SHA-256 of r without buffering it in RAM, returning the
// hex digest and the total byte count.
func Reader(r io.Reader) (digest string, size int64, err error) {
	return Copy(r, io.Discard)
}

// File streams path through SHA-256 without buffering it in RAM.
func File(path string) (digest string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	return Reader(f)
}
