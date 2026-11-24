package processor

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// ParentFinalizer is the minimal orchestration boundary for assembling a
// completed chunk parent. Queue ownership is acquired atomically before any
// media work begins.
type ParentQueue interface {
	ClaimFinalization(context.Context, string) (*queue.Job, bool, error)
	Children(context.Context, string) ([]*queue.Job, error)
	Complete(context.Context, string, int64, queue.Artifact) error
	Renew(context.Context, string, int64) error
}

type ParentFinalizer struct {
	queue     ParentQueue
	store     *storage.Client
	assembler chronon.Assembler
	publisher drive.Publisher
	workerID  string
	outputDir string
}

func NewParentFinalizer(q ParentQueue, store *storage.Client, assembler chronon.Assembler, publisher drive.Publisher, workerID, outputDir string) *ParentFinalizer {
	return &ParentFinalizer{queue: q, store: store, assembler: assembler, publisher: publisher, workerID: workerID, outputDir: outputDir}
}

// Finalize attempts one parent finalization for a caller that already knows the
// frame range the child family must cover. It returns finalized=false when
// children are incomplete or another worker owns finalization.
//
// The validation happens twice — once against the pre-claim read, once against
// a fresh read after the atomic claim — so a child family that changes between
// the optimistic check and the ownership hand-off is re-checked before any
// assembly work. ValidateChildren needs no expected-count parameter: dense
// chunk indices plus exact half-open frame coverage of [start, end) are the
// verifiable completeness contract (see queue.ValidateChildren).
func (f *ParentFinalizer) Finalize(ctx context.Context, parentID string, start, end int64) (bool, queue.Artifact, error) {
	children, err := f.queue.Children(ctx, parentID)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	return f.finalize(ctx, parentID, start, end, children)
}

// FinalizeFromChildren is the worker's completion-path entry point. The
// expected frame range is derived from the family read and validated again
// after the atomic claim. Families with no ranges are ordinary run-correlation
// children, not assembly input; mixed or partial ranges remain errors.
func (f *ParentFinalizer) FinalizeFromChildren(ctx context.Context, parentID string) (bool, queue.Artifact, error) {
	children, err := f.queue.Children(ctx, parentID)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	start, end, ok := childFrameRange(children)
	if !ok {
		if len(children) == 0 || allChildrenUnranged(children) {
			// ParentJobID also correlates ordinary render jobs with a PipelineGen
			// run. Those families are published by their submitter and are not
			// packet-copy chunk assemblies. Treat them as not-applicable instead
			// of claiming the parent and logging a false "no frame range" error.
			return false, queue.Artifact{}, nil
		}
		return false, queue.Artifact{}, fmt.Errorf("parent finalizer: child family of %s has no complete frame range to assemble", parentID)
	}
	return f.finalize(ctx, parentID, start, end, children)
}

func allChildrenUnranged(children []*queue.Job) bool {
	if len(children) == 0 {
		return true
	}
	for _, child := range children {
		if child == nil || child.FrameRange != nil {
			return false
		}
	}
	return true
}

// childFrameRange returns the half-open [start, end) frame range a child family
// spans, or false when the family is empty or a boundary child carries no frame
// range. It deliberately takes the family as an argument: the range is only
// meaningful relative to the read it came from.
func childFrameRange(children []*queue.Job) (int64, int64, bool) {
	if len(children) == 0 {
		return 0, 0, false
	}
	first, last := children[0], children[len(children)-1]
	if first == nil || last == nil || first.FrameRange == nil || last.FrameRange == nil {
		return 0, 0, false
	}
	return first.FrameRange.Start, last.FrameRange.End, true
}

// finalize runs the finalization protocol against an already-read pre-claim
// child family, so the caller never pays for the same read twice.
func (f *ParentFinalizer) finalize(ctx context.Context, parentID string, start, end int64, children []*queue.Job) (bool, queue.Artifact, error) {
	claimedJob, claimed, children, err := f.claimParentFinalization(ctx, parentID, start, end, children)
	if err != nil || !claimed {
		return false, queue.Artifact{}, err
	}
	workCtx, stopLease := f.startParentLeaseRenewal(ctx, parentID, claimedJob)
	defer stopLease()
	if err := f.prepareParentOutput(); err != nil {
		return false, queue.Artifact{}, err
	}
	inputs, err := f.parentInputPaths(workCtx, children)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	output := filepath.Join(f.outputDir, parentID+".mp4")
	// The assembled parent is a temporary staging file: it is uploaded below
	// and must never accumulate on the worker. A failed removal is logged.
	defer func() {
		if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
			log.Printf("parent %s: remove staging output %s: %v", parentID, output, err)
		}
	}()
	assembleReq, err := f.parentAssembleRequest(workCtx, parentID, output, children, claimedJob, inputs)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	if err := f.assembler.Assemble(workCtx, assembleReq); err != nil {
		return false, queue.Artifact{}, err
	}
	artifact, err := f.persistAndPublishParent(workCtx, parentID, output)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	if err := workCtx.Err(); err != nil {
		return false, queue.Artifact{}, err
	}
	if err := f.queue.Complete(workCtx, parentID, claimedJob.LeaseGeneration, artifact); err != nil {
		return false, queue.Artifact{}, err
	}
	return true, artifact, nil
}

// claimParentFinalization validates the pre-claim family, acquires the atomic
// finalization lease and re-validates the family the claim exposed.
func (f *ParentFinalizer) claimParentFinalization(ctx context.Context, parentID string, start, end int64, children []*queue.Job) (*queue.Job, bool, []*queue.Job, error) {
	if err := queue.ValidateChildren(children, start, end); err != nil {
		return nil, false, nil, err
	}
	claimedJob, claimed, err := f.queue.ClaimFinalization(ctx, parentID)
	if err != nil || !claimed {
		return nil, false, nil, err
	}
	children, err = f.queue.Children(ctx, parentID)
	if err != nil {
		return nil, false, nil, err
	}
	if err := queue.ValidateChildren(children, start, end); err != nil {
		return nil, false, nil, err
	}
	return claimedJob, true, children, nil
}

// startParentLeaseRenewal renews the parent lease until the caller stops it.
// The cleanup always cancels the work context and waits for the renewer.
func (f *ParentFinalizer) startParentLeaseRenewal(ctx context.Context, parentID string, claimedJob *queue.Job) (context.Context, func()) {
	workCtx, cancel := context.WithCancel(ctx)
	if claimedJob == nil || claimedJob.Lease <= 0 {
		return workCtx, cancel
	}
	interval := claimedJob.Lease / 2
	if interval <= 0 {
		interval = time.Second
	}
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-ticker.C:
				if err := f.queue.Renew(workCtx, parentID, claimedJob.LeaseGeneration); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return workCtx, func() {
		cancel()
		<-renewDone
	}
}

// prepareParentOutput validates the finalizer dependencies and creates the
// staging directory.
func (f *ParentFinalizer) prepareParentOutput() error {
	if f.assembler == nil {
		return fmt.Errorf("parent finalizer: assembler is required")
	}
	if f.outputDir == "" {
		return fmt.Errorf("parent finalizer: output directory is required")
	}
	return os.MkdirAll(f.outputDir, 0o755)
}

// parentInputPaths materialises every verified child artifact in queue order.
func (f *ParentFinalizer) parentInputPaths(ctx context.Context, children []*queue.Job) ([]string, error) {
	inputs := make([]string, 0, len(children))
	for _, child := range children {
		path, _, err := f.store.LocalPath(ctx, child.Artifact.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("chunk %d local path: %w", child.ChunkIndex, err)
		}
		inputs = append(inputs, path)
	}
	return inputs, nil
}

// parentAssembleRequest builds the fragmented/audio-aware assembly request.
func (f *ParentFinalizer) parentAssembleRequest(ctx context.Context, parentID, output string, children []*queue.Job, claimedJob *queue.Job, inputs []string) (chronon.AssembleRequest, error) {
	fragmented := true
	for _, child := range children {
		if child.Artifact.AudioStreams > 0 {
			fragmented = false
			break
		}
	}
	audio, err := anchorAudioSource(claimedJob)
	if err != nil {
		return chronon.AssembleRequest{}, err
	}
	assembleReq := chronon.AssembleRequest{Inputs: inputs, Output: output, FragmentedMP4: fragmented}
	if audio.want {
		if f.store == nil {
			return chronon.AssembleRequest{}, fmt.Errorf("parent finalizer: anchor declares source audio but no store is wired to materialise it")
		}
		audioPath, _, err := f.store.LocalPath(ctx, audio.sourceHash)
		if err != nil {
			return chronon.AssembleRequest{}, fmt.Errorf("parent finalizer: anchor audio source: %w", err)
		}
		assembleReq.AudioSourcePath = audioPath
		assembleReq.AudioTargetSampleRate = audio.targetSampleRate
		// An audio-bearing parent keeps the classic MP4 whatever the children
		// report: the moov backpatch happens here, and the tail-upload lane
		// must never stream a file whose header is still open.
		assembleReq.FragmentedMP4 = false
	}
	return assembleReq, nil
}

// persistAndPublishParent stores the assembled artifact and publishes it
// through the same verified path as a segment artifact.
func (f *ParentFinalizer) persistAndPublishParent(ctx context.Context, parentID, output string) (queue.Artifact, error) {
	artifact, err := persistParentArtifact(ctx, f.store, parentID, output)
	if err != nil {
		return queue.Artifact{}, err
	}
	if f.publisher != nil {
		published, err := publishAndVerify(ctx, f.store, f.publisher, parentID+".mp4", artifact)
		if err != nil {
			return queue.Artifact{}, err
		}
		artifact.DriveFileID, artifact.DriveLink = published.FileID, published.WebViewLink
	}
	return artifact, nil
}

// anchorAudio is the family anchor's single source-audio mux request, derived
// from the anchor's semantic plan document — the FULL mapping the producer
// submits for the anchor (children carry the video-only mapping).
type anchorAudio struct {
	sourceHash       string
	targetSampleRate int
	want             bool
}

// anchorAudioSource is the SINGLE typed authority for the finalizer's audio
// mux. It reads the anchor plan through overlay.CompileSemantic — the same
// compiler the render path uses — so the audio policy (Plan.Output.Audio) and
// the source asset (the layer with id "source", matched against the compile's
// content-addressed asset list) are interpreted exactly once and exactly the
// way a single-job render interprets them.
//
// Fail-closed: an undecodable plan, an audio declaration with no source
// layer, or a source layer whose asset is missing from the compile is an
// error — never a silently silent parent that would break the clip artifact
// contract. A plan that declares NO audio legitimately returns want=false:
// the family stays silent and fragmented.
func anchorAudioSource(job *queue.Job) (anchorAudio, error) {
	if job == nil || len(job.RenderPlan) == 0 {
		return anchorAudio{}, nil
	}
	result, err := overlay.CompileSemantic(job.RenderPlan)
	if err != nil {
		return anchorAudio{}, fmt.Errorf("parent finalizer: compile anchor plan: %w", err)
	}
	if result.Plan == nil || result.Plan.Output.Audio == nil || result.Plan.Output.Audio.Mode == "" {
		return anchorAudio{}, nil
	}
	for _, layer := range result.Plan.Layers {
		if layer.ID != "source" || layer.Source == "" {
			continue
		}
		for _, asset := range result.Assets {
			if asset.LogicalPath == layer.Source {
				return anchorAudio{
					sourceHash:       asset.Hash,
					targetSampleRate: result.Plan.Output.Audio.SampleRate,
					want:             true,
				}, nil
			}
		}
		return anchorAudio{}, fmt.Errorf("parent finalizer: anchor declares source audio but source asset %q is not in the compile asset set", layer.Source)
	}
	return anchorAudio{}, fmt.Errorf("parent finalizer: anchor declares source audio but the plan has no source layer to mux from")
}
