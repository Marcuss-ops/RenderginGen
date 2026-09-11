// multilingual.go owns the Strategy-A runtime binding: the base job renders
// the shared video pass once; every per-language overlay job then consumes
// the base ARTIFACT as its source video. The queue knows nothing about this
// relationship — it is expressed through ordinary content-addressed assets —
// so Chronon needs zero changes.
package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// SourceBindLogicalPath is the workspace path where the base artifact is
// materialized for an overlay job. Overlay plans reference the localized
// source under this logical path.
const SourceBindLogicalPath = "videos/bound_base.mp4"

// OverlayJobID returns the derived queue job ID for a language overlay pass.
func OverlayJobID(b BatchID, baseLogicalID, language string) string {
	return jobID(b, baseLogicalID+".overlay."+language)
}

// overlayPlanSourcePatch rewrites the language overlay plan's source to point
// at the bound base artifact. The semantic plan's source block declares the
// asset_id/sha256 of the video the overlay pass composites over; the binder
// replaces it with the base artifact's coordinates so the worker materializes
// the base video into the overlay job's workspace.
//
// The patch is mechanical (no semantic decisions): it keeps every other field
// of the plan byte-identical.
func overlayPlanSourcePatch(plan []byte, baseHash string) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(plan, &doc); err != nil {
		return nil, fmt.Errorf("batch: decode overlay plan for source binding: %w", err)
	}
	source := map[string]json.RawMessage{}
	if raw, ok := doc["source"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &source); err != nil {
			return nil, fmt.Errorf("batch: decode overlay plan source block: %w", err)
		}
	}
	// asset_id stays semantic ("bound_base"); the sha256 is the content
	// address the worker resolves through the ordinary asset store.
	source["asset_id"] = json.RawMessage(`"bound_base"`)
	source["sha256"] = json.RawMessage(`"` + baseHash + `"`)
	patchedSource, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	doc["source"] = patchedSource
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

// OverlayBinding is one language's post-base submission unit.
type OverlayBinding struct {
	Language string
	Job      queue.Job
}

// BaseOutcome carries what the submitter needs from the completed base job.
type BaseOutcome struct {
	JobID string
	// ArtifactHash is the content address of the rendered base video.
	ArtifactHash string
}

// AwaitBase polls the queue until the base job reaches a terminal state and
// returns its artifact hash. A failed/cancelled base aborts the batch: there
// is nothing to overlay onto.
func AwaitBase(ctx context.Context, s Submitter, baseJobID string, poll time.Duration) (BaseOutcome, error) {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for {
		job, err := s.Get(ctx, baseJobID)
		if err != nil {
			return BaseOutcome{}, fmt.Errorf("batch: await base %s: %w", baseJobID, err)
		}
		switch job.State {
		case queue.StateCompleted:
			if job.Artifact == nil || job.Artifact.ArtifactHash == "" {
				return BaseOutcome{}, fmt.Errorf("batch: base job %s completed without an artifact hash", baseJobID)
			}
			return BaseOutcome{JobID: baseJobID, ArtifactHash: job.Artifact.ArtifactHash}, nil
		case queue.StateFailed, queue.StateCancelled:
			return BaseOutcome{}, fmt.Errorf("batch: base job %s reached terminal state %s; overlay passes aborted", baseJobID, job.State)
		}
		select {
		case <-ctx.Done():
			return BaseOutcome{}, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// BindOverlayJobs patches every language job's plan with the base artifact
// hash, appends the base artifact as a content-addressed asset, and recomputes
// each job's idempotency key over the PATCHED plan: replaying the same bound
// batch resolves to the same jobs, while a re-rendered base (new hash)
// legitimately yields new overlay jobs.
func BindOverlayJobs(b BatchID, jobs []queue.Job, base BaseOutcome) ([]queue.Job, error) {
	out := make([]queue.Job, 0, len(jobs))
	for _, j := range jobs {
		if j.ParentJobID == "" {
			// The base job itself: drop it from the overlay wave.
			continue
		}
		patched, err := overlayPlanSourcePatch(j.RenderPlan, base.ArtifactHash)
		if err != nil {
			return nil, err
		}
		j.RenderPlan = patched
		j.Assets = append(j.Assets, queue.AssetRef{
			Hash:        base.ArtifactHash,
			LogicalPath: SourceBindLogicalPath,
		})
		// Recompute the key over the final content (patched plan + assets).
		logical := j.ID
		if idx := strings.IndexByte(j.ID, ':'); idx >= 0 {
			logical = j.ID[idx+1:]
		}
		j.IdempotencyKey = idempotencyKey(b, logical, j.RenderPlan, j.Assets)
		out = append(out, j)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("batch: no overlay jobs to bind (base %s)", base.JobID)
	}
	return out, nil
}
