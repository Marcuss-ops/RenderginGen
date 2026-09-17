// Command objectstore runs the central object store (L3 artifact storage).
package main

import (
	"crypto/subtle"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/buildinfo"
	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/server"
	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/store"
)

// authMiddleware enforces a shared bearer token when one is configured. The
// store exposes arbitrary read/write over HTTP; unauthenticated access from
// any host that can reach the port is not acceptable in production. When
// OBJECTSTORE_TOKEN is unset the store stays open (local/dev posture).
func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) > len(prefix) && strings.HasPrefix(auth, prefix) &&
			subtle.ConstantTimeCompare([]byte(auth[len(prefix):]), []byte(token)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func main() {
	defaultAddr := os.Getenv("OBJECTSTORE_ADDR")
	if defaultAddr == "" {
		defaultAddr = ":9000"
	}
	addr := flag.String("addr", defaultAddr, "listen address")
	dataDir := flag.String("data-dir", "/var/lib/objectstore", "data directory")
	verifyAll := flag.Bool("verify-all", false,
		"verify every stored object against its content address, report staging leftovers, and exit non-zero on any violation (read-only; does not serve)")
	flag.Parse()

	// Publish the process identity before anything can fail, so /health answers
	// "which object store binary, which commit, which store contract is this?"
	// The store is the third service in the render path and the last one to be
	// able to identify itself; without it an operator can read the revision of
	// the PipelineGen server and the RenderingGen worker but has to guess which
	// build is behind :9000.
	//
	// There is no config FILE for this service (it is configured entirely by
	// flags and environment), so config_path stays empty rather than being
	// pointed at something that is not a config — absence is reported, never
	// fabricated.
	workerID, hostErr := os.Hostname()
	if hostErr != nil {
		workerID = "unknown"
	}
	buildinfo.SetRuntime(buildinfo.RuntimeInfo{Mode: "objectstore", WorkerID: workerID})

	st := store.New(*dataDir)

	// Operator integrity audit. The write path proves that objects THIS
	// process accepted hash to their address; it says nothing about a store
	// directory restored from a backup, synced from another host or copied by
	// hand. This is the check an operator runs after such an operation, and
	// the reason it is read-only is that a check which repairs what it finds
	// cannot be used as evidence.
	if *verifyAll {
		report, err := st.Scan()
		if err != nil {
			log.Fatalf("integrity scan failed: %v", err)
		}
		log.Printf("integrity scan: data-dir=%s objects=%d verified=%d violations=%d staging=%d",
			*dataDir, report.Objects, report.Verified, len(report.Failures), len(report.Staging))
		for _, failure := range report.Failures {
			log.Printf("INTEGRITY VIOLATION key=%s path=%s reason=%s", failure.Key, failure.Path, failure.Reason)
		}
		for _, path := range report.Staging {
			log.Printf("STAGING LEFTOVER path=%s", path)
		}
		if !report.Clean() {
			log.Fatalf("integrity scan found %d violation(s) and %d staging leftover(s): the store is holding bytes that do not match their addresses",
				len(report.Failures), len(report.Staging))
		}
		return
	}
	srv := server.New(st)
	srv.SetBuildInfo(buildinfo.Current)
	var handler http.Handler = srv.Handler()
	if token := os.Getenv("OBJECTSTORE_TOKEN"); token != "" {
		handler = authMiddleware(token, handler)
		log.Printf("object store auth: enabled (OBJECTSTORE_TOKEN)")
	} else {
		log.Printf("object store auth: disabled (set OBJECTSTORE_TOKEN to require a bearer token)")
	}

	log.Printf("object store listening on %s (data-dir=%s)", *addr, *dataDir)
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute, // large artifact uploads
		WriteTimeout:      10 * time.Minute, // large artifact downloads
		IdleTimeout:       120 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
