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
	"github.com/Marcuss-ops/RenderginGen/queue/internal/postgres"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/server"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	lease := flag.Duration("lease", 30*time.Second, "job lease duration")
	maxAttempts := flag.Int("max-attempts", 3, "max attempts before a job is permanently failed")
	expireInterval := flag.Duration("expire-interval", 5*time.Second, "lease expiry scan interval")
	dbURL := flag.String("db-url", "", "PostgreSQL DSN; enables the postgres repository when set (defaults to $DATABASE_URL)")
	flag.Parse()

	repo := repository.JobRepository(store.New(*lease, *maxAttempts))
	if dsn := databaseURL(*dbURL); dsn != "" {
		db, err := openDatabase(dsn)
		if err != nil {
			log.Fatalf("connect database: %v", err)
		}
		defer db.Close()

		if err := migrate.Apply(context.Background(), db); err != nil {
			log.Fatalf("migrate database: %v", err)
		}
		repo = postgres.New(db, *lease, *maxAttempts)
		log.Printf("using postgres job repository")
	}

	// Background lease expiry: requeue jobs whose lease elapsed.
	go func() {
		ticker := time.NewTicker(*expireInterval)
		defer ticker.Stop()
		for range ticker.C {
			n, err := repo.RequeueExpired(time.Now())
			if err != nil {
				log.Printf("requeue expired: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("requeued %d jobs with expired lease", n)
			}
		}
	}()

	srv := server.New(repo)
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

// openDatabase opens and verifies a PostgreSQL connection.
func openDatabase(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
