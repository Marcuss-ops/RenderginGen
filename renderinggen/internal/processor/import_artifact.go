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
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
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
	ItemIDs           []string        `json:"item_ids,omitempty"`
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
	if request.Producer == "pipelinegen-map-v2" && len(request.ItemIDs) != 1 {
		return request, fmt.Errorf("processor: pipelinegen map import requires exactly one item_id")
	}
	seenItemIDs := make(map[string]struct{}, len(request.ItemIDs))
	for _, itemID := range request.ItemIDs {
		if itemID == "" || itemID != strings.TrimSpace(itemID) {
			return request, fmt.Errorf("processor: overlay.import item_ids must be non-empty and trimmed")
		}
		if _, exists := seenItemIDs[itemID]; exists {
			return request, fmt.Errorf("processor: overlay.import item_ids must be unique")
		}
		seenItemIDs[itemID] = struct{}{}
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
	request, asset, err := validateOverlayImportJob(job)
	if err != nil {
		return queue.Artifact{}, err
	}
	workerlog.BindJob(job.ParentJobID, job.ID)
	ws, cleanup, err := p.openImportWorkspace(ctx, job, asset)
	if err != nil {
		return queue.Artifact{}, err
	}
	defer cleanup()
	localPath, err := ws.AssetPath(asset.LogicalPath)
	if err != nil {
		return queue.Artifact{}, err
	}
	sha, size, staged, identityDuration, err := p.resolveImportedIdentity(ctx, job.ID, localPath, request.SourceSHA256, request.SourceSizeBytes)
	if err != nil {
		return queue.Artifact{}, err
	}
	probe, probeDuration, err := probeImportedMP4(ctx, sha, localPath, request)
	if err != nil {
		return queue.Artifact{}, err
	}
	metrics := importedArtifactMetrics(request, identityDuration, probeDuration)
	artifact := p.newImportedArtifact(job, request, sha, size, probe, metrics)
	if !staged {
		if err := p.store.PutFile(ctx, sha, localPath); err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: persist imported artifact: %w", err)
		}
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

func validateOverlayImportJob(job *queue.Job) (overlayImportEnvelope, queue.AssetRef, error) {
	if err := validate(job); err != nil {
		return overlayImportEnvelope{}, queue.AssetRef{}, err
	}
	if job.JobType != queue.JobTypeOverlayImport {
		return overlayImportEnvelope{}, queue.AssetRef{}, fmt.Errorf("processor: import requires job type %q", queue.JobTypeOverlayImport)
	}
	request, err := decodeOverlayImport(job.RenderPlan)
	if err != nil {
		return overlayImportEnvelope{}, queue.AssetRef{}, err
	}
	if len(job.Assets) != 1 {
		return overlayImportEnvelope{}, queue.AssetRef{}, fmt.Errorf("processor: overlay.import requires exactly one source asset")
	}
	asset := job.Assets[0]
	if !strings.EqualFold(asset.Hash, request.SourceSHA256) || asset.Hash != request.SourceSHA256 {
		return overlayImportEnvelope{}, queue.AssetRef{}, fmt.Errorf("processor: import asset hash %q does not match request source_sha256 %q", asset.Hash, request.SourceSHA256)
	}
	if path.Ext(asset.LogicalPath) != ".mp4" {
		return overlayImportEnvelope{}, queue.AssetRef{}, fmt.Errorf("processor: overlay.import source logical_path must end in .mp4")
	}
	return request, asset, nil
}

func (p *Processor) openImportWorkspace(ctx context.Context, job *queue.Job, asset queue.AssetRef) (*workspace.Workspace, func(), error) {
	ws, err := workspace.New(p.jobsRoot, job.ID+"-import")
	if err != nil {
		return nil, nil, err
	}
	if err := ws.WriteLease(time.Now().Add(p.workspaceLeaseDuration())); err != nil {
		p.cleanupWorkspace(ws, job.ID)
		return nil, nil, fmt.Errorf("processor: establish import workspace lease: %w", err)
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
	cleanup := func() {
		close(refreshDone)
		p.cleanupWorkspace(ws, job.ID)
	}
	if err := ws.MaterializePaths(ctx, p.resolveAssetStreaming, []queue.AssetRef{asset}); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("processor: materialize imported map MP4: %w", err)
	}
	return ws, cleanup, nil
}

func (p *Processor) resolveImportedIdentity(ctx context.Context, jobID, localPath, wantSHA string, wantSize int64) (sha string, size int64, staged bool, took time.Duration, err error) {
	identityStarted := time.Now()
	if p.store != nil {
		stagedSHA, stagedSize, stageErr := p.store.PutStagedFile(ctx, stagingSessionID(jobID+"-import:"+localPath), localPath)
		switch {
		case stageErr == nil:
			sha, size, staged = stagedSHA, stagedSize, true
			workerlog.ByJobID(jobID).Infof("imported map identity from the staged upload stream (bytes=%d)", stagedSize)
		case errors.Is(stageErr, storage.ErrStagingUnsupported):
			// Memory/test backends keep the classic hash-then-PUT path.
		default:
			// A failed optimization must not fail an import whose bytes are
			// intact: the classic path re-reads and publishes the same file.
			workerlog.ByJobID(jobID).Warnf("import staged upload failed; falling back to hash-then-PUT: %v", stageErr)
		}
	}
	if !staged {
		sha, size, err = hashio.File(localPath)
		if err != nil {
			return "", 0, false, 0, fmt.Errorf("processor: hash imported map MP4: %w", err)
		}
	}
	if size != wantSize {
		return "", 0, false, 0, fmt.Errorf("processor: imported MP4 size %d does not match request %d", size, wantSize)
	}
	if sha != wantSHA {
		return "", 0, false, 0, fmt.Errorf("processor: imported MP4 sha256 %s does not match request %s", sha, wantSHA)
	}
	return sha, size, staged, time.Since(identityStarted), nil
}

func probeImportedMP4(ctx context.Context, sha, localPath string, request overlayImportEnvelope) (media.ProbeResult, time.Duration, error) {
	probeStarted := time.Now()
	// The content hash is verified above, so it is the probe cache key:
	// re-importing identical bytes reuses the certified facts.
	probe, err := media.ProbeFileCached(ctx, sha, localPath)
	if err != nil {
		return media.ProbeResult{}, 0, fmt.Errorf("processor: probe imported map MP4: %w", err)
	}
	if err := probe.ValidateOverlay(request.Width, request.Height, request.FPSNum, request.FPSDen); err != nil {
		return media.ProbeResult{}, 0, fmt.Errorf("processor: imported map media contract: %w", err)
	}
	if probe.FrameCount != request.FrameCount {
		return media.ProbeResult{}, 0, fmt.Errorf("processor: imported map frame_count %d does not match producer %d", probe.FrameCount, request.FrameCount)
	}
	if delta := probe.DurationUS - request.DurationUS; delta < -int64(request.FPSDen)*1_000_000/int64(request.FPSNum) || delta > int64(request.FPSDen)*1_000_000/int64(request.FPSNum) {
		return media.ProbeResult{}, 0, fmt.Errorf("processor: imported map duration %dus differs from producer %dus by more than one frame", probe.DurationUS, request.DurationUS)
	}
	if err := media.ValidateDecode(ctx, localPath); err != nil {
		return media.ProbeResult{}, 0, fmt.Errorf("processor: fully decode imported map MP4: %w", err)
	}
	return probe, time.Since(probeStarted), nil
}

func importedArtifactMetrics(request overlayImportEnvelope, identityDuration, probeDuration time.Duration) map[string]float64 {
	probeUS := probeDuration.Microseconds()
	metrics := map[string]float64{
		metricnames.ImportIdentityUS: float64(identityDuration.Microseconds()),
		metricnames.ProbeUS:          float64(probeUS),
		metricnames.ProbeMS:          float64(probeUS) / 1000,
		metricnames.ImportVerified:   1,
	}
	if len(request.ItemIDs) > 0 {
		metrics[metricnames.OverlayItemCount] = float64(len(request.ItemIDs))
		if len(request.ItemIDs) > 1 {
			metrics[metricnames.OverlayRenderBatch] = 1
		}
	}
	return metrics
}

func (p *Processor) newImportedArtifact(job *queue.Job, request overlayImportEnvelope, sha string, size int64, probe media.ProbeResult, metrics map[string]float64) queue.Artifact {
	return queue.Artifact{
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
			ItemIDs:          append([]string(nil), request.ItemIDs...),
			IdentityVerified: true, StructureVerified: true, FullDecodeVerified: true,
			ProducerTelemetry: append(json.RawMessage(nil), request.ProducerTelemetry...),
		},
	}
}
