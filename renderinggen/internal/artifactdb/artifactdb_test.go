package artifactdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func sampleRecord(jobID string) ArtifactRecord {
	return ArtifactRecord{
		JobID:               jobID,
		ArtifactHash:        "abc123",
		StorageKey:          "abc123",
		SizeBytes:           1024,
		ContentType:         "video/mp4",
		Backend:             "software",
		ChrononVersion:      "0.1.0",
		ProfileID:           "velox-h264-1080p30-v1",
		Container:           "mp4",
		Codec:               "h264",
		CodecProfile:        "High",
		PixelFormat:         "yuv420p",
		Width:               1920,
		Height:              1080,
		FPSNum:              30,
		FPSDen:              1,
		FrameCount:          150,
		DurationUS:          5_000_000,
		AudioStreams:        0,
		EntityCount:         2,
		ImportantPhraseCnt:  1,
		ImportantWordCnt:    1,
		ImageCount:          1,
		LightLeakCount:      1,
		PresetID:            "impact_mix_v1",
		OverlayCompileUS:    1000,
		AssetMaterializeUS:  2000,
		ChrononRenderUS:     30_000,
		SHA256US:            500,
		ObjectStoreUploadUS: 700,
		DriveUploadUS:       4000,
		TotalUS:             40_000,
		InputBytes:          2048,
		OutputBytes:         1024,
		CreatedAt:           time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	}
}

func TestMemoryRecorderRoundTrip(t *testing.T) {
	rec := NewMemory()
	ctx := context.Background()
	if err := rec.Record(ctx, sampleRecord("job-1")); err != nil {
		t.Fatalf("record: %v", err)
	}
	if rec.Len() != 1 {
		t.Fatalf("len = %d, want 1", rec.Len())
	}
	got, ok := rec.Get("job-1")
	if !ok {
		t.Fatal("record not found")
	}
	if got.ArtifactHash != "abc123" || got.EntityCount != 2 || got.PresetID != "impact_mix_v1" {
		t.Fatalf("record mismatch: %+v", got)
	}
	if _, ok := rec.Get("missing"); ok {
		t.Fatal("missing job must not resolve")
	}
}

func TestMemoryRecorderUpsertAndUpdateDrive(t *testing.T) {
	rec := NewMemory()
	ctx := context.Background()
	if err := rec.Record(ctx, sampleRecord("job-1")); err != nil {
		t.Fatalf("record: %v", err)
	}
	// Idempotent re-record (publication retry) replaces, never duplicates.
	if err := rec.Record(ctx, sampleRecord("job-1")); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	if rec.Len() != 1 {
		t.Fatalf("len = %d after upsert, want 1", rec.Len())
	}
	// Drive retry only touches the drive metric.
	if err := rec.UpdateDrive(ctx, "job-1", 9999); err != nil {
		t.Fatalf("update drive: %v", err)
	}
	got, _ := rec.Get("job-1")
	if got.DriveUploadUS != 9999 {
		t.Fatalf("drive_upload_us = %d, want 9999", got.DriveUploadUS)
	}
	if got.ArtifactHash != "abc123" {
		t.Fatalf("drive update must not touch artifact identity: %+v", got)
	}
	// Update on a missing job is a no-op, not an error.
	if err := rec.UpdateDrive(ctx, "missing", 1); err != nil {
		t.Fatalf("update missing: %v", err)
	}
}

func TestMetricsProjection(t *testing.T) {
	m := sampleRecord("job-1").Metrics()
	want := map[string]float64{
		"entity_count": 2, "important_phrase_count": 1, "important_word_count": 1,
		"image_count": 1, "light_leak_count": 1,
		"overlay_compile_us": 1000, "asset_materialize_us": 2000,
		"chronon_render_us": 30000,
		"sha256_us":         500, "objectstore_upload_us": 700, "drive_upload_us": 4000,
		"total_us": 40000, "input_bytes": 2048, "output_bytes": 1024,
		"frame_count": 150, "duration_us": 5000000, "width": 1920, "height": 1080, "fps": 30,
	}
	for k, v := range want {
		if got := m[k]; got != v {
			t.Errorf("metrics[%s] = %v, want %v", k, got, v)
		}
	}
}

func TestSQLiteRecorderRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts.db")
	rec, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rec.Close()
	ctx := context.Background()
	if err := rec.Record(ctx, sampleRecord("job-1")); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := rec.Record(ctx, sampleRecord("job-2")); err != nil {
		t.Fatalf("record: %v", err)
	}
	// Re-record job-1: upsert, not a second row.
	if err := rec.Record(ctx, sampleRecord("job-1")); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	var count int
	if err := rec.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifact_records`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("rows = %d, want 2 (upsert must not duplicate)", count)
	}
	var got ArtifactRecord
	var created string
	if err := rec.db.QueryRowContext(ctx, `SELECT job_id, artifact_hash, size_bytes, entity_count, preset_id, drive_upload_us, created_at FROM artifact_records WHERE job_id='job-1'`).
		Scan(&got.JobID, &got.ArtifactHash, &got.SizeBytes, &got.EntityCount, &got.PresetID, &got.DriveUploadUS, &created); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.ArtifactHash != "abc123" || got.EntityCount != 2 || got.PresetID != "impact_mix_v1" || got.DriveUploadUS != 4000 {
		t.Fatalf("row mismatch: %+v", got)
	}
	if created == "" {
		t.Fatal("created_at empty")
	}
}

func TestSQLiteRecorderChrononTelemetryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts.db")
	rec, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rec.Close()
	ctx := context.Background()

	r := sampleRecord("job-1")
	r.ChrononTelemetry = json.RawMessage(`{"job":{"plan_compile_ms":4.1,"graph_compile_ms":2.2},"summary":{"p50_frame_ms":12.3}}`)
	if err := rec.Record(ctx, r); err != nil {
		t.Fatalf("record: %v", err)
	}
	var got string
	if err := rec.db.QueryRowContext(ctx, `SELECT chronon_telemetry FROM artifact_records WHERE job_id='job-1'`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != string(r.ChrononTelemetry) {
		t.Fatalf("chronon_telemetry = %q, want %q", got, r.ChrononTelemetry)
	}
}

func TestSQLiteRecorderMigratesExistingLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts.db")
	// Simulate a ledger created before chronon_telemetry existed: a table
	// with the pre-migration columns only.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	const oldSchema = `
CREATE TABLE artifact_records (
  job_id              TEXT PRIMARY KEY,
  artifact_hash       TEXT NOT NULL,
  storage_key         TEXT NOT NULL,
  size_bytes          INTEGER NOT NULL,
  content_type        TEXT NOT NULL DEFAULT '',
  backend             TEXT NOT NULL DEFAULT '',
  chronon_version     TEXT NOT NULL DEFAULT '',
  profile_id          TEXT NOT NULL DEFAULT '',
  container           TEXT NOT NULL DEFAULT '',
  codec               TEXT NOT NULL DEFAULT '',
  codec_profile       TEXT NOT NULL DEFAULT '',
  pixel_format        TEXT NOT NULL DEFAULT '',
  width               INTEGER NOT NULL DEFAULT 0,
  height              INTEGER NOT NULL DEFAULT 0,
  fps_num             INTEGER NOT NULL DEFAULT 0,
  fps_den             INTEGER NOT NULL DEFAULT 0,
  frame_count         INTEGER NOT NULL DEFAULT 0,
  duration_us         INTEGER NOT NULL DEFAULT 0,
  audio_streams       INTEGER NOT NULL DEFAULT 0,
  first_frame_keyframe INTEGER NOT NULL DEFAULT 0,
  entity_count        INTEGER NOT NULL DEFAULT 0,
  important_phrase_count INTEGER NOT NULL DEFAULT 0,
  important_word_count INTEGER NOT NULL DEFAULT 0,
  image_count         INTEGER NOT NULL DEFAULT 0,
  light_leak_count    INTEGER NOT NULL DEFAULT 0,
  preset_id           TEXT NOT NULL DEFAULT '',
  overlay_compile_us  INTEGER NOT NULL DEFAULT 0,
  asset_materialize_us INTEGER NOT NULL DEFAULT 0,
  chronon_render_us   INTEGER NOT NULL DEFAULT 0,
  encode_us           INTEGER NOT NULL DEFAULT 0,
  sha256_us           INTEGER NOT NULL DEFAULT 0,
  objectstore_upload_us INTEGER NOT NULL DEFAULT 0,
  drive_upload_us     INTEGER NOT NULL DEFAULT 0,
  total_us            INTEGER NOT NULL DEFAULT 0,
  input_bytes         INTEGER NOT NULL DEFAULT 0,
  output_bytes        INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL
);`
	if _, err := raw.Exec(oldSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	// Reopen through NewSQLite: the migration must add the column in place.
	rec, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("open migrated: %v", err)
	}
	defer rec.Close()
	if err := rec.Record(context.Background(), sampleRecord("job-1")); err != nil {
		t.Fatalf("record on migrated ledger: %v", err)
	}
}

func TestSQLiteRecorderConcurrentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts.db")
	rec, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rec.Close()
	// The production post pool records from several goroutines at once; a
	// lock error here would fail otherwise-healthy jobs.
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := rec.Record(context.Background(), sampleRecord(fmt.Sprintf("job-%02d", i)))
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent record: %v", err)
	}
	var count int
	if err := rec.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM artifact_records`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n {
		t.Fatalf("rows = %d, want %d", count, n)
	}
}

func TestSQLiteRecorderUpdateDrive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts.db")
	rec, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rec.Close()
	ctx := context.Background()
	if err := rec.Record(ctx, sampleRecord("job-1")); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := rec.UpdateDrive(ctx, "job-1", 7777); err != nil {
		t.Fatalf("update drive: %v", err)
	}
	var drive int64
	var hash string
	if err := rec.db.QueryRowContext(ctx, `SELECT drive_upload_us, artifact_hash FROM artifact_records WHERE job_id='job-1'`).Scan(&drive, &hash); err != nil {
		t.Fatalf("select: %v", err)
	}
	if drive != 7777 {
		t.Fatalf("drive_upload_us = %d, want 7777", drive)
	}
	if hash != "abc123" {
		t.Fatalf("artifact_hash = %q, want abc123", hash)
	}
	// Update of a missing job is a no-op.
	if err := rec.UpdateDrive(ctx, "missing", 1); err != nil {
		t.Fatalf("update missing: %v", err)
	}
}
