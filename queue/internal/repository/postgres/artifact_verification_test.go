package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestArtifactVerificationRoundTripsThroughTheLedger pins the durable proof
// block: a consumer must be able to tell "semantically certified" from
// "integrity-only but unverifiable" without reading worker logs.
func TestArtifactVerificationRoundTripsThroughTheLedger(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	ctx := context.Background()

	if err := r.Submit(ctx, model.Job{ID: "verification-job"}); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := r.Claim(ctx, "renderer")
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	artifact := model.Artifact{
		ID:           "artifact-verification",
		Kind:         "overlay",
		StorageKey:   "sha-verification",
		ArtifactHash: "sha-verification",
		Provenance:   &model.ArtifactProvenance{SchemaVersion: "renderinggen.artifact-provenance.v1"},
		Verification: &model.ArtifactVerification{
			Policy:      "certify",
			Decode:      model.VerificationDecodeFull,
			Composition: model.VerificationCompositionVerified,
			Evidence:    model.VerificationEvidenceChrononSummary,
		},
	}
	if err := r.Rendered(ctx, claimed.ID, "renderer", claimed.LeaseGeneration, artifact, "deferred"); err != nil {
		t.Fatalf("Rendered: %v", err)
	}

	got, err := r.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Artifact == nil || got.Artifact.Verification == nil {
		t.Fatalf("artifact verification missing after Rendered: %+v", got.Artifact)
	}
	if *got.Artifact.Verification != *artifact.Verification {
		t.Fatalf("verification = %+v, want %+v", got.Artifact.Verification, artifact.Verification)
	}

	// The publisher re-claims the rendered job and must receive the same block.
	publisher, _, err := r.ClaimState(ctx, "publisher", model.StateRendered)
	if err != nil || publisher == nil {
		t.Fatalf("re-claim = %+v, %v", publisher, err)
	}
	if publisher.Artifact == nil || publisher.Artifact.Verification == nil ||
		publisher.Artifact.Verification.Composition != model.VerificationCompositionVerified {
		t.Fatalf("claim hydration lost the verification block: %+v", publisher.Artifact)
	}

	if err := r.Complete(ctx, publisher.ID, "publisher", publisher.LeaseGeneration, artifact); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	done, err := r.Get(ctx, publisher.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Artifact == nil || done.Artifact.Verification == nil || done.Artifact.Verification.Policy != "certify" {
		t.Fatalf("completed artifact lost its verification block: %+v", done.Artifact)
	}
}
