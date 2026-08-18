package artifactdb

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func sampleRecord(jobID string) ArtifactRecord {
	return ArtifactRecord{
		JobID:            jobID,
		ArtifactHash:     "abc123",
		StorageKey:       "abc123",
		SizeBytes:        1024,
		ContentType:      "video/mp4",
		Backend:          "software",
		ChrononVersion:   "0.1.0",
		ProfileID:        "velox-h264-1080p30-v1",
		Container:        "mp4",
		Codec:            "h264",
		CodecProfile:     "High",
		PixelFormat:      "yuv420p",
		Width:            1920,
		Height:           1080,
		FPSNum:           30,
		FPSDen:           1,
		FrameCount:       150,
		DurationUS:       5_000_000,
		AudioStreams:     0,
		EntityCount:      2,
		ImportantPhraseCnt: 1,
		ImportantWordCnt: 1,
		ImageCount:       1,
		LightLeakCount:   1,
		PresetID:         "impact_mix_v1",
		OverlayCompileUS: 1000,
		AssetMaterializeUS: 2000,
		ChrononRenderUS:  30_000,
		EncodeUS:         0,
		SHA256US:         500,
		ObjectStoreUploadUS: 700,
		DriveUploadUS:    4000,
		TotalUS:          40_000,
		InputBytes:       2048,
		OutputBytes:      1024,
		CreatedAt:        time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
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
		"chronon_render_us": 30000, "encode_us": 0,
		"sha256_us": 500, "objectstore_upload_us": 700, "drive_upload_us": 4000,
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
