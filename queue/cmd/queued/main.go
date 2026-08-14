// Command queued runs the central pull-based job queue.
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/server"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	lease := flag.Duration("lease", 30*time.Second, "job lease duration")
	maxAttempts := flag.Int("max-attempts", 3, "max attempts before a job is permanently failed")
	expireInterval := flag.Duration("expire-interval", 5*time.Second, "lease expiry scan interval")
	dbURL := flag.String("db-url", "", "PostgreSQL DSN; migrations run at startup when set (defaults to $DATABASE_URL)")
	flag.Parse()

	if dsn := databaseURL(*dbURL); dsn != "" {
		if err := migrateDatabase(dsn); err != nil {
			log.Fatalf("migrate database: %v", err)
		}
		log.Printf("database migrations up to date")
	}

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

// databaseURL returns the explicit flag value, falling back to $DATABASE_URL.
func databaseURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("DATABASE_URL")
}

// migrateDatabase connects to PostgreSQL and applies pending migrations.
func migrateDatabase(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return err
	}
	return migrate.Apply(ctx, db)
}
