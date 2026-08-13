// Command queued runs the central pull-based job queue.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/server"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/store"
)

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	lease := flag.Duration("lease", 30*time.Second, "job lease duration")
	maxAttempts := flag.Int("max-attempts", 3, "max attempts before a job is permanently failed")
	expireInterval := flag.Duration("expire-interval", 5*time.Second, "lease expiry scan interval")
	flag.Parse()

	st := store.New(*lease, *maxAttempts)

	// Background lease expiry: requeue jobs whose lease elapsed.
	go func() {
		ticker := time.NewTicker(*expireInterval)
		defer ticker.Stop()
		for range ticker.C {
			if n := st.RequeueExpired(time.Now()); n > 0 {
				log.Printf("requeued %d jobs with expired lease", n)
			}
		}
	}()

	srv := server.New(st)
	log.Printf("job queue listening on %s (lease=%s, max-attempts=%d)", *addr, *lease, *maxAttempts)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
