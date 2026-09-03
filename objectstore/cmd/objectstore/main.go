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

	"github.com/Marcuss-ops/RenderginGen/objectstore/internal/server"
	"github.com/Marcuss-ops/RenderginGen/objectstore/internal/store"
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
	addr := flag.String("addr", "127.0.0.1:9000", "listen address")
	dataDir := flag.String("data-dir", "/var/lib/objectstore", "data directory")
	flag.Parse()

	st := store.New(*dataDir)
	var handler http.Handler = server.New(st).Handler()
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
