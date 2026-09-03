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

	"github.com/Marcuss-ops/RenderginGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository/postgres"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/server"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/service"
	"github.com/jackc/pgx/v5"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address")
	lease := flag.Duration("lease", 10*time.Minute, "job lease duration")
	maxAttempts := flag.Int("max-attempts", 3, "max attempts before a job is permanently failed")
	expireInterval := flag.Duration("expire-interval", 5*time.Second, "lease expiry scan interval")
	workerStale := flag.Duration("worker-stale-after", 90*time.Second, "worker heartbeat staleness threshold")
	dbURL := flag.String("db-url", "", "PostgreSQL DSN; enables the postgres repository when set (defaults to $DATABASE_URL)")
	flag.Parse()

	// In-memory is the default backend; PostgreSQL is used when configured.
	// Both backends implement the job and worker contracts.
	memRepo := memory.New(*lease, *maxAttempts)
	repo := repository.JobRepository(memRepo)
	var workerRepo repository.WorkerRepository = memRepo
	var postgresDSN string
	if dsn := databaseURL(*dbURL); dsn != "" {
		db, err := openDatabase(dsn)
		if err != nil {
			log.Fatalf("connect database: %v", err)
		}
		// Bound the pool: unbounded, one connection per concurrent long-poll
		// claim would let connection count scale with worker count instead of
		// load. Postgres claim transactions are short (SKIP LOCKED single-row)
		// so a modest pool is enough; the LISTEN session is separate.
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(10)
		db.SetConnMaxIdleTime(5 * time.Minute)
		db.SetConnMaxLifetime(time.Hour)
		defer db.Close()

		if err := migrate.Apply(context.Background(), db); err != nil {
			log.Fatalf("migrate database: %v", err)
		}
		pgRepo := postgres.New(db, *lease, *maxAttempts)
		repo = pgRepo
		workerRepo = pgRepo
		postgresDSN = dsn
		log.Printf("using postgres job repository")
	}

	svc := service.New(repo)
	svc.SetWorkerRepository(workerRepo, *workerStale)
	m := metrics.New()
	svc.SetMetrics(m)

	// Background lease expiry: requeue jobs whose lease elapsed.
	go func() {
		ticker := time.NewTicker(*expireInterval)
		defer ticker.Stop()
		for range ticker.C {
			n, err := svc.RequeueExpired(time.Now())
			if err != nil {
				log.Printf("requeue expired: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("requeued %d jobs with expired lease", n)
			}
			// Refresh worker liveness so ready/offline gauges decay as
			// heartbeats age without requiring a new heartbeat.
			svc.RefreshWorkerHealth()
		}
	}()

	srv := server.New(svc)
	srv.SetMetricsHandler(m.Handler())
	if postgresDSN != "" {
		// Every queue replica owns a dedicated LISTEN connection. Migration 016
		// emits rendering_jobs notifications whenever a row becomes claimable;
		// the notification only wakes local long-poll requests. The subsequent
		// repository claim remains the source of truth and uses SKIP LOCKED.
		go listenForJobNotifications(context.Background(), postgresDSN, srv)
	}
	log.Printf("job queue listening on %s (lease=%s, max-attempts=%d)", *addr, *lease, *maxAttempts)
	server := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// listenForJobNotifications keeps one lightweight PostgreSQL LISTEN session
// per queue replica and reconnects after transient database/network failures.
func listenForJobNotifications(ctx context.Context, dsn string, srv *server.Server) {
	const retryDelay = 2 * time.Second
	for ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			log.Printf("queue notify connect: %v", err)
			if !sleepContext(ctx, retryDelay) {
				return
			}
			continue
		}

		if _, err := conn.Exec(ctx, "LISTEN rendering_jobs"); err != nil {
			log.Printf("queue notify LISTEN: %v", err)
			_ = conn.Close(context.Background())
			if !sleepContext(ctx, retryDelay) {
				return
			}
			continue
		}
		log.Printf("queue notifications listening on rendering_jobs")

		for ctx.Err() == nil {
			notification, err := conn.WaitForNotification(ctx)
			if err != nil {
				log.Printf("queue notify wait: %v", err)
				break
			}
			srv.NotifyState(model.State(notification.Payload))
		}
		_ = conn.Close(context.Background())
		if !sleepContext(ctx, retryDelay) {
			return
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
