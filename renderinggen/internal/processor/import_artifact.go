package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

const overlayImportSchema = "renderinggen.overlay-import.v1"
const overlayImportProvenanceSchema = "renderinggen.artifact-provenance.v1"

// overlayImportEnvelope is the versioned cross-service attestation request.
// Producer telemetry is opaque JSON: RenderingGen preserves its source but
// does not reinterpret producer-side timings as Chronon measurements.
type overlayImportEnvelope struct {
	SchemaVersion     string          `json:"schema_version"`
	Producer          string          `json:"producer"`
	PlanID            string          `json:"plan_id"`
	VideoID           string          `json:"video_id"`
	SourceSHA256      string          `json:"source_sha256"`
	SourceSizeBytes   int64           `json:"source_size_bytes"`
	Width             int             `json:"width"`
	Height            int             `json:"height"`
	FPSNum            int             `json:"fps_num"`
	FPSDen            int             `json:"fps_den"`
	DurationUS        int64           `json:"duration_us"`
	FrameCount        int             `json:"frame_count"`
	ProducerTelemetry json.RawMessage `json:"producer_telemetry,omitempty"`
}

func decodeOverlayImport(raw []byte) (overlayImportEnvelope, error) {
	var request overlayImportEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("processor: decode overlay.import: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return request, fmt.Errorf("processor: overlay.import contains trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return request, fmt.Errorf("processor: decode overlay.import trailing data: %w", err)
	}
	if request.SchemaVersion != overlayImportSchema || strings.TrimSpace(request.Producer) == "" ||
		strings.TrimSpace(request.PlanID) == "" || strings.TrimSpace(request.VideoID) == "" {
		return request, fmt.Errorf("processor: overlay.import schema, producer, plan_id and video_id are required")
	}
	if len(request.SourceSHA256) != 64 || strings.Trim(request.SourceSHA256, "0123456789abcdef") != "" {
		return request, fmt.Errorf("processor: overlay.import source_sha256 must be lowercase SHA-256")
	}
	if request.SourceSizeBytes <= 0 || request.Width <= 0 || request.Height <= 0 || request.FPSNum <= 0 ||
		request.FPSDen <= 0 || request.DurationUS <= 0 || request.FrameCount <= 0 {
		return request, fmt.Errorf("processor: overlay.import byte size and media contract must be positive")
	}
	if len(request.ProducerTelemetry) > 0 && !json.Valid(request.ProducerTelemetry) {
		return request, fmt.Errorf("processor: overlay.import producer_telemetry is invalid JSON")
	}
	return request, nil
}

// ImportOverlayArtifact verifies an already-staged producer MP4 without calling
// Chronon. The worker independently hashes the materialized bytes, performs a
// structural probe and a complete CPU decode, and stores/ledgers the same byte
// identity. No producer assertion can substitute for these checks.
func (p *Processor) ImportOverlayArtifact(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	started := time.Now()
	if err := validate(job); err != nil {
		return queue.Artifact{}, err
	}
	if job.JobType != queue.JobTypeOverlayImport {
		return queue.Artifact{}, fmt.Errorf("processor: import requires job type %q", queue.JobTypeOverlayImport)
	}
	request, err := decodeOverlayImport(job.RenderPlan)
	if err != nil {
		return queue.Artifact{}, err
	}
	if len(job.Assets) != 1 {
		return queue.Artifact{}, fmt.Errorf("processor: overlay.import requires exactly one source asset")
	}
	asset := job.Assets[0]
	if !strings.EqualFold(asset.Hash, request.SourceSHA256) || asset.Hash != request.SourceSHA256 {
		return queue.Artifact{}, fmt.Errorf("processor: import asset hash %q does not match request source_sha256 %q", asset.Hash, request.SourceSHA256)
	}
	if path.Ext(asset.LogicalPath) != ".mp4" {
		return queue.Artifact{}, fmt.Errorf("processor: overlay.import source logical_path must end in .mp4")
	}
	workerlog.BindJob(job.ParentJobID, job.ID)
	ws, err := workspace.New(p.jobsRoot, job.ID+"-import")
	if err != nil {
		return queue.Artifact{}, err
	}
	if err := ws.WriteLease(time.Now().Add(p.workspaceLeaseDuration())); err != nil {
		p.cleanupWorkspace(ws, job.ID)
		return queue.Artifact{}, fmt.Errorf("processor: establish import workspace lease: %w", err)
	}
	refreshDone := make(chan struct{})
	go func() {
		refreshEvery := p.workspaceLeaseDuration() / 3
		if refreshEvery <= 0 {
			refreshEvery = 10 * time.Minute
		}
		ticker := time.NewTicker(refreshEvery)
		defer ticker.Stop()
		for {
			select {
			case <-refreshDone:
				return
			case <-ticker.C:
				if err := ws.WriteLease(time.Now().Add(p.workspaceLeaseDuration())); err != nil {
					workerlog.ByJobID(job.ID).Warnf("refresh import workspace lease marker: %v", err)
				}
			}
		}
	}()
	defer func() {
		close(refreshDone)
		p.cleanupWorkspace(ws, job.ID)
	}()
	if err := ws.MaterializePaths(ctx, p.resolveAssetStreaming, []queue.AssetRef{asset}); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: materialize imported map MP4: %w", err)
	}
	localPath, err := ws.AssetPath(asset.LogicalPath)
	if err != nil {
		return queue.Artifact{}, err
	}
	identityStarted := time.Now()
	sha, size, err := hashio.File(localPath)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: hash imported map MP4: %w", err)
	}
	if size != request.SourceSizeBytes {
		return queue.Artifact{}, fmt.Errorf("processor: imported MP4 size %d does not match request %d", size, request.SourceSizeBytes)
	}
	if sha != request.SourceSHA256 {
		return queue.Artifact{}, fmt.Errorf("processor: imported MP4 sha256 %s does not match request %s", sha, request.SourceSHA256)
	}
	probeStarted := time.Now()
	probe, err := media.ProbeFile(ctx, localPath)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: probe imported map MP4: %w", err)
	}
	if err := probe.ValidateOverlay(request.Width, request.Height, request.FPSNum, request.FPSDen); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: imported map media contract: %w", err)
	}
	if probe.FrameCount != request.FrameCount {
		return queue.Artifact{}, fmt.Errorf("processor: imported map frame_count %d does not match producer %d", probe.FrameCount, request.FrameCount)
	}
	if delta := probe.DurationUS - request.DurationUS; delta < -int64(request.FPSDen)*1_000_000/int64(request.FPSNum) || delta > int64(request.FPSDen)*1_000_000/int64(request.FPSNum) {
		return queue.Artifact{}, fmt.Errorf("processor: imported map duration %dus differs from producer %dus by more than one frame", probe.DurationUS, request.DurationUS)
	}
	if err := media.ValidateDecode(ctx, localPath); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: fully decode imported map MP4: %w", err)
	}
	probeUS := time.Since(probeStarted).Microseconds()
	metrics := map[string]float64{
		metricnames.ImportIdentityUS: float64(time.Since(identityStarted).Microseconds()),
		metricnames.ProbeUS:          float64(probeUS),
		metricnames.ProbeMS:          float64(probeUS) / 1000,
		metricnames.ImportVerified:   1,
	}
	artifact := queue.Artifact{
		Kind: "overlay_import", StorageKey: sha, ArtifactURL: p.artifactURL(sha), ArtifactHash: sha,
		ContentType: "video/mp4", SizeBytes: size,
		Width: probe.Width, Height: probe.Height, FPSNum: probe.FPSNum, FPSDen: probe.FPSDen,
		FrameCount: probe.FrameCount, DurationUS: probe.DurationUS,
		Codec: probe.VideoCodec, CodecProfile: probe.CodecProfile, Container: probe.Container,
		PixelFormat: probe.PixelFormat, AudioStreams: probe.AudioStreams, OutputFacts: outputFactsFromProbe(&probe),
		Backend: "pipelinegen_map_import", ChrononVersion: "", CopyEligible: false,
		Metrics: metrics,
		Provenance: &queue.ArtifactProvenance{
			SchemaVersion: overlayImportProvenanceSchema, Protocol: overlayImportSchema,
			Producer: request.Producer, ImportJobID: job.ID, SourceSHA256: sha, SourceSizeBytes: size,
			IdentityVerified: true, StructureVerified: true, FullDecodeVerified: true,
			ProducerTelemetry: append(json.RawMessage(nil), request.ProducerTelemetry...),
		},
	}
	// PipelineGen's worker already staged these exact bytes under their content
	// address. PutFile verifies the content-addressed L3 write and warms L2;
	// there is no re-encode or byte transformation on this path.
	if err := p.store.PutFile(ctx, sha, localPath); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: persist imported artifact: %w", err)
	}
	metrics[metricnames.ImportTotalUS] = float64(time.Since(started).Microseconds())
	if p.recorder != nil {
		artifact, err = p.recordArtifact(ctx, job.ID, artifact, &probe, overlay.Stats{}, size, nil)
		if err != nil {
			return queue.Artifact{}, err
		}
	}
	workerlog.ByJobID(job.ID).Infof("producer artifact imported and verified: sha256=%s size=%d frames=%d %dx%d fps=%d/%d; Chronon was not invoked",
		sha, size, probe.FrameCount, probe.Width, probe.Height, probe.FPSNum, probe.FPSDen)
	return artifact, nil
}
