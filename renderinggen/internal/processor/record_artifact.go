// record_artifact.go owns the worker-local artifact ledger mirror and the
// small identity helpers shared by the store/publish phases (published backend
// naming, stable canvas metadata, artifact object URL).
package processor

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// recordArtifact writes an optional worker-local diagnostic mirror. PostgreSQL
// queue completion remains authoritative, so mirror failures are non-fatal:
// the rendered artifact is returned unchanged except for the mirror_failure
// metric, which carries the observability signal to the completed job.
func (p *Processor) recordArtifact(ctx context.Context, jobID string, artifact queue.Artifact, probe *media.ProbeResult, stats overlay.Stats, inputBytes int64, chrononTelemetry json.RawMessage) (queue.Artifact, error) {
	if p.recorder == nil {
		return artifact, nil
	}
	rec := artifactdb.ArtifactRecord{
		JobID:              jobID,
		ArtifactHash:       artifact.ArtifactHash,
		StorageKey:         artifact.StorageKey,
		SizeBytes:          artifact.SizeBytes,
		ContentType:        artifact.ContentType,
		Backend:            artifact.Backend,
		ChrononVersion:     artifact.ChrononVersion,
		ProfileID:          artifact.ProfileID,
		EntityCount:        stats.EntityCount,
		ImportantPhraseCnt: stats.ImportantPhraseCnt,
		ImportantWordCnt:   stats.ImportantWordCnt,
		ImageCount:         stats.ImageCount,
		LightLeakCount:     stats.LightLeakCount,
		PresetID:           stats.PresetID,
		InputBytes:         inputBytes,
		OutputBytes:        artifact.SizeBytes,
		ChrononTelemetry:   chrononTelemetry,
		// Mirror of the raw deep-profile sidecar reference on the queue
		// artifact (content-addressed object-store preservation).
		ChrononTimingStorageKey:  artifact.ChrononTimingStorageKey,
		ChrononTimingURL:         artifact.ChrononTimingURL,
		ChrononTimingSHA256:      artifact.ChrononTimingSHA256,
		ChrononTimingSizeBytes:   artifact.ChrononTimingSizeBytes,
		ChrononTimingContentType: artifact.ChrononTimingContentType,
		CreatedAt:                time.Now().UTC(),
	}
	if probe != nil {
		rec.Container = probe.Container
		rec.Codec = probe.VideoCodec
		rec.CodecProfile = probe.CodecProfile
		rec.PixelFormat = probe.PixelFormat
		rec.Width = probe.Width
		rec.Height = probe.Height
		rec.FPSNum = probe.FPSNum
		rec.FPSDen = probe.FPSDen
		rec.FrameCount = probe.FrameCount
		rec.DurationUS = probe.DurationUS
		rec.AudioStreams = probe.AudioStreams
		rec.FirstFrameKeyframe = probe.FirstFrameKeyframe
	}
	// The metric names are the vocabulary constants (internal/metricnames),
	// not literals: the same names are emitted by the pipeline, projected by
	// the local mirror and persisted in the queue's processing_metrics, and the
	// vocabulary test proves no emitter can invent a name.
	if m, ok := artifact.Metrics[metricnames.OverlayCompileUS]; ok {
		rec.OverlayCompileUS = int64(m)
	}
	if m, ok := artifact.Metrics[metricnames.AssetMaterializeUS]; ok {
		rec.AssetMaterializeUS = int64(m)
	}
	if m, ok := artifact.Metrics[metricnames.RenderUS]; ok {
		rec.ChrononRenderUS = int64(m)
	}
	if m, ok := artifact.Metrics[metricnames.SHA256US]; ok {
		rec.SHA256US = int64(m)
	}
	if m, ok := artifact.Metrics[metricnames.ObjectStoreUploadUS]; ok {
		rec.ObjectStoreUploadUS = int64(m)
	}
	if m, ok := artifact.Metrics[metricnames.DriveUploadUS]; ok {
		rec.DriveUploadUS = int64(m)
	}
	if m, ok := artifact.Metrics[metricnames.TotalUS]; ok {
		rec.TotalUS = int64(m)
	}
	if err := p.recorder.Record(ctx, rec); err != nil {
		// The mirror must never fail the render: PostgreSQL queue completion is
		// authoritative. Surface the divergence on the artifact itself so the
		// completed job's metrics (visible on GET /jobs/{id} and benchmark
		// reports) carry an observable signal instead of a silent gap.
		log.Printf("processor: artifact diagnostic mirror %s unavailable: %v", jobID, err)
		if artifact.Metrics == nil {
			artifact.Metrics = map[string]float64{}
		}
		artifact.Metrics[metricnames.MirrorFailure] = 1
	}
	return artifact, nil
}

// PublishedBackendChrononVulkan is the published identity of the certified
// Chronon GPU path. It is a PROJECTION of a PipelineGen contract value, not a
// second definition: the owner is the clip.render backend vocabulary
// (refactored/internal/capabilities/cliprender/backend.go,
// BackendChrononVulkan).
//
// It is declared here because the worker must EMAIL the value on the artifact
// boundary and the queue module cannot import PipelineGen; the literal is
// pinned back to its owner by published_backend_contract_test.go, which fails
// if the two ever disagree (and skips, visibly, when the sibling is absent).
const PublishedBackendChrononVulkan = "chronon_vulkan"

// publishedRenderBackend is the cross-service backend identity. RenderingGen
// uses "vulkan" internally for configuration, while PipelineGen's clip.render
// contract names the certified Chronon path "chronon_vulkan". Only a render
// that passed the strict native receipt gate may receive that identity.
// GPU-composited host-frame pipe renders remain Vulkan internally, but are not
// native-surface certifications.
func publishedRenderBackend(backend string, nativeCertified bool) string {
	if backend == "vulkan" && nativeCertified {
		return PublishedBackendChrononVulkan
	}
	return backend
}

type planMetadata struct {
	Width      int
	Height     int
	FPSNum     int
	FPSDen     int
	FrameCount int
	DurationUS int64
	ProfileID  string
}

// renderMetadata extracts only the stable canvas facts needed by the
// artifact record, from the ONE typed plan (no JSON re-parsing).
func planMetadataOf(plan *overlay.Plan) planMetadata {
	if plan == nil || plan.Canvas.FPSNum <= 0 || plan.Canvas.FPSDen <= 0 {
		return planMetadata{}
	}
	return planMetadata{
		Width: plan.Canvas.Width, Height: plan.Canvas.Height,
		FPSNum: plan.Canvas.FPSNum, FPSDen: plan.Canvas.FPSDen,
		FrameCount: int(plan.Canvas.DurationFrames),
		DurationUS: plan.Canvas.DurationFrames * 1_000_000 * int64(plan.Canvas.FPSDen) / int64(plan.Canvas.FPSNum),
		ProfileID:  plan.Output.ProfileID,
	}
}

// artifactURL returns the L3 object URL for an artifact hash.
func (p *Processor) artifactURL(hash string) string {
	if p.storeURL == "" {
		return ""
	}
	return p.storeURL + "/objects/" + hash
}
