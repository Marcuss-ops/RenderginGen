// Command objectstore runs the central object store (L3 artifact storage).
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/Marcuss-ops/RenderginGen/objectstore/internal/server"
	"github.com/Marcuss-ops/RenderginGen/objectstore/internal/store"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	dataDir := flag.String("data-dir", "/var/lib/objectstore", "data directory")
	flag.Parse()

	st := store.New(*dataDir)
	srv := server.New(st)
	log.Printf("object store listening on %s (data-dir=%s)", *addr, *dataDir)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
