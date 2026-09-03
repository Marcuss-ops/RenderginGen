package migrate

import (
	"testing"

	"github.com/Marcuss-ops/RenderginGen/queue/migrations"
)

func TestSplitStatements(t *testing.T) {
	script := "-- header comment; with semicolon\n" +
		"CREATE TABLE a (id text DEFAULT 'x;y');\n\n" +
		"CREATE TABLE b (id int); -- trailing; comment\n"
	got := splitStatements(script)
	want := []string{
		"CREATE TABLE a (id text DEFAULT 'x;y')",
		"CREATE TABLE b (id int)",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d statements, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statement %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestMigrationsOrdered(t *testing.T) {
	names, err := migrations.Names()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"001_render_jobs.sql",
		"002_render_attempts.sql",
		"003_render_artifacts.sql",
		"004_render_workers.sql",
		"005_render_events.sql",
		"006_processing_metrics.sql",
		"007_indexes.sql",
		"008_worker_heartbeats.sql",
		"009_render_plan.sql",
		"010_artifact_render_meta.sql",
		"011_idempotency_index.sql",
		"012_render_rendered_state.sql",
		"013_artifact_media_contract.sql",
		"014_chunk_parent_index.sql",
		"015_parent_finalizing_state.sql",
		"016_render_job_notifications.sql",
		"017_render_job_progress.sql",
	}
	if len(names) != len(want) {
		t.Fatalf("want %d migrations, got %d: %v", len(want), len(names), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("migration %d: want %q, got %q", i, want[i], names[i])
		}
	}
}
