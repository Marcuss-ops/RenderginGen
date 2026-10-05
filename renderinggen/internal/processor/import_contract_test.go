package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

func makeOverlayImportJob(t *testing.T, path, id string) (*queue.Job, []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	plan := overlayImportEnvelope{
		SchemaVersion: overlayImportSchema, Producer: "pipelinegen-map-v1", PlanID: "map-plan", VideoID: "video-1",
		SourceSHA256: hash, SourceSizeBytes: int64(len(data)), Width: 1280, Height: 720,
		FPSNum: 30, FPSDen: 1, DurationUS: 8_000_000, FrameCount: 240,
		ProducerTelemetry: json.RawMessage(`{"schema":"chronon.dynamic-map-telemetry.v1","renderer_wall_s":1.2}`),
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return &queue.Job{
		ID: id, Schema: queue.JobSchemaV1, Version: queue.JobSchemaVersionV1,
		JobType: queue.JobTypeOverlayImport, RenderPlan: raw,
		Assets: []queue.AssetRef{{Hash: hash, LogicalPath: "assets/semantic/map.mp4"}},
	}, data
}

func TestDecodeOverlayImportRejectsUnknownFieldsAndMismatchedIdentity(t *testing.T) {
	valid := `{"schema_version":"renderinggen.overlay-import.v1","producer":"pipelinegen-map-v1","plan_id":"p","video_id":"v","source_sha256":"` + strings.Repeat("a", 64) + `","source_size_bytes":1,"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_us":1000000,"frame_count":30}`
	if _, err := decodeOverlayImport([]byte(valid)); err != nil {
		t.Fatalf("valid import contract rejected: %v", err)
	}
	for _, tc := range []struct{ name, raw string }{
		{"unknown field", strings.TrimSuffix(valid, "}") + `,"not_in_schema":true}`},
		{"trailing JSON", valid + ` {}`},
		{"uppercase hash", strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("A", 64), 1)},
		{"missing decode identity", strings.Replace(valid, `"frame_count":30`, `"frame_count":0`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeOverlayImport([]byte(tc.raw)); err == nil {
				t.Fatal("malformed import request accepted")
			}
		})
	}
}

func TestImportOverlayArtifactIsByteIdenticalAndDoesNotInvokeChronon(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe unavailable: %v", err)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg unavailable: %v", err)
	}
	fixture := filepath.Join("..", "..", "..", "testdata", "golden", "background.mp4")
	job, expected := makeOverlayImportJob(t, fixture, "map-import-e2e")
	store := storage.New(storage.NewMemory(), storage.Options{L2Dir: t.TempDir()})
	// Stage the exact producer MP4 as PipelineGen does before queue submission.
	if err := store.Put(context.Background(), job.Assets[0].Hash, expected); err != nil {
		t.Fatal(err)
	}
	renderer := &fakeRenderer{}
	proc := New(t.TempDir(), "software", "chronon-not-used", "http://store.invalid", store, renderer)

	artifact, err := proc.ImportOverlayArtifact(context.Background(), job)
	if err != nil {
		t.Fatalf("import verified producer artifact: %v", err)
	}
	if renderer.calls != 0 {
		t.Fatalf("import invoked Chronon %d times", renderer.calls)
	}
	if artifact.Kind != "overlay_import" || artifact.Backend != "pipelinegen_map_import" || artifact.ChrononVersion != "" {
		t.Fatalf("import was misidentified as a Chronon render: %+v", artifact)
	}
	if artifact.CopyEligible || artifact.ProfileID != "" {
		t.Fatalf("import claimed a separate copy profile without certification: %+v", artifact)
	}
	if artifact.ArtifactHash != job.Assets[0].Hash || artifact.StorageKey != job.Assets[0].Hash || artifact.SizeBytes != int64(len(expected)) {
		t.Fatalf("import artifact identity = %+v", artifact)
	}
	stored, err := store.Get(context.Background(), artifact.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(expected) {
		t.Fatal("stored import bytes differ from the producer MP4")
	}
	if artifact.OutputFacts == nil || artifact.OutputFacts.Width != 1280 || artifact.FrameCount == 0 {
		t.Fatalf("import lacks structural output certification: %+v", artifact.OutputFacts)
	}
	if artifact.Provenance == nil || !artifact.Provenance.IdentityVerified || !artifact.Provenance.StructureVerified || !artifact.Provenance.FullDecodeVerified || artifact.Provenance.Producer != "pipelinegen-map-v1" {
		t.Fatalf("import provenance incomplete: %+v", artifact.Provenance)
	}
	if _, ok := artifact.Metrics["render_ms"]; ok {
		t.Fatalf("CPU import fabricated Chronon render timing: %v", artifact.Metrics)
	}
	if _, ok := artifact.Metrics["encode_ms"]; ok {
		t.Fatalf("CPU import fabricated Chronon encode timing: %v", artifact.Metrics)
	}
}

func TestImportOverlayArtifactFailsClosedOnBadIdentity(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe unavailable: %v", err)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg unavailable: %v", err)
	}
	fixture := filepath.Join("..", "..", "..", "testdata", "golden", "background.mp4")
	job, source := makeOverlayImportJob(t, fixture, "map-import-bad-hash")
	bad := []byte("not-the-producer-bytes")
	store := storage.New(storage.NewMemory(), storage.Options{L2Dir: t.TempDir()})
	if err := store.Put(context.Background(), job.Assets[0].Hash, bad); err != nil {
		t.Fatal(err)
	}
	proc := New(t.TempDir(), "software", "", "", store, &fakeRenderer{})
	if _, err := proc.ImportOverlayArtifact(context.Background(), job); err == nil {
		t.Fatal("import accepted bytes whose object-store identity differs from the producer")
	}
	if len(source) == 0 {
		t.Fatal("fixture source should be non-empty")
	}
}
