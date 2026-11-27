package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// storeArtifactWithPolicy runs the real storeArtifact path with one policy and
// one plan shape. It returns the artifact (only on success) and the backend so a
// caller can assert whether bytes reached the store.
func storeArtifactWithPolicy(t *testing.T, policy renderVerifyLevel, plan *overlay.Plan, sidecar bool) (queue.Artifact, *storage.Memory) {
	t.Helper()
	backend := storage.NewMemory()
	proc := &Processor{
		store:          storage.New(backend, storage.Options{}),
		receiptVerify:  policy,
		chrononVersion: "test",
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(output, []byte("verification matrix bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sidecar {
		body, err := json.Marshal(map[string]any{
			"schema":  chronon.TimingSidecarSchemaV2,
			"version": 2,
			"job": map[string]any{
				"execution_path": "full_graph_native",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output+chronon.TimingSidecarSuffix, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	artifact, err := proc.storeArtifact(context.Background(), storeArtifactInput{
		jobID:        "verification-" + string(policy),
		outputPath:   output,
		plan:         plan,
		phaseMetrics: map[string]float64{},
		totalStart:   time.Now(),
	})
	if err != nil {
		t.Fatalf("storeArtifact(%s, overlay=%t, sidecar=%t): %v", policy, planHasVisualOverlay(plan), sidecar, err)
	}
	return artifact, backend
}

func verificationOverlayPlan() *overlay.Plan {
	return &overlay.Plan{
		Canvas: overlay.Canvas{Width: 64, Height: 64, FPSNum: 25, FPSDen: 1, DurationFrames: 25},
		Layers: []overlay.Layer{
			{ID: "source", Type: "video"},
			{ID: "title", Type: "text", Text: "authored overlay"},
		},
	}
}

func verificationSourceOnlyPlan() *overlay.Plan {
	return &overlay.Plan{
		Canvas: overlay.Canvas{Width: 64, Height: 64, FPSNum: 25, FPSDen: 1, DurationFrames: 25},
		Layers: []overlay.Layer{{ID: "source", Type: "video"}},
	}
}

// TestStoreArtifactRecordsVerificationVerdict covers the policy x proof matrix
// through the real publication path: the verdict is derived from the same gate
// that accepts or rejects the job, so it cannot drift into a second opinion.
func TestStoreArtifactRecordsVerificationVerdict(t *testing.T) {
	t.Run("certify overlay with summary is verified", func(t *testing.T) {
		artifact, _ := storeArtifactWithPolicy(t, renderVerifyCertify, verificationOverlayPlan(), true)
		v := artifact.Verification
		if v == nil {
			t.Fatal("certify success recorded no verification block")
		}
		if v.Policy != "certify" || v.Decode != queue.VerificationDecodeFull ||
			v.Composition != queue.VerificationCompositionVerified ||
			v.Evidence != queue.VerificationEvidenceChrononSummary {
			t.Fatalf("verification = %+v", v)
		}
	})

	t.Run("strict without overlay is not required", func(t *testing.T) {
		artifact, _ := storeArtifactWithPolicy(t, renderVerifyStrict, verificationSourceOnlyPlan(), false)
		v := artifact.Verification
		if v == nil || v.Composition != queue.VerificationCompositionNotRequired {
			t.Fatalf("verification = %+v, want not_required", v)
		}
		if v.Evidence != queue.VerificationEvidenceNone {
			t.Fatalf("evidence = %q, want none", v.Evidence)
		}
	})

	t.Run("normal overlay without summary is unverifiable but published", func(t *testing.T) {
		artifact, backend := storeArtifactWithPolicy(t, renderVerifyNormal, verificationOverlayPlan(), false)
		v := artifact.Verification
		if v == nil || v.Composition != queue.VerificationCompositionUnverifiable {
			t.Fatalf("verification = %+v, want unverifiable", v)
		}
		if _, err := backend.Fetch(context.Background(), artifact.ArtifactHash); err != nil {
			t.Fatalf("fail-open artifact was not published: %v", err)
		}
	})
}

// TestStoreArtifactCertifyFailsWithoutCompositionEvidence pins the missing
// sidecar case: before this, the fail-closed check only ran when the optional
// summary was readable, so certify silently accepted a plan whose composition
// was never proven.
func TestStoreArtifactCertifyFailsWithoutCompositionEvidence(t *testing.T) {
	for _, policy := range []renderVerifyLevel{renderVerifyCertify, renderVerifyStrict} {
		t.Run(string(policy), func(t *testing.T) {
			backend := storage.NewMemory()
			proc := &Processor{
				store:          storage.New(backend, storage.Options{}),
				receiptVerify:  policy,
				chrononVersion: "test",
			}
			dir := t.TempDir()
			output := filepath.Join(dir, "out.mp4")
			if err := os.WriteFile(output, []byte("must not be certified"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := proc.storeArtifact(context.Background(), storeArtifactInput{
				jobID:        "certify-no-evidence",
				outputPath:   output,
				plan:         verificationOverlayPlan(),
				phaseMetrics: map[string]float64{},
				totalStart:   time.Now(),
			})
			if err == nil {
				t.Fatalf("%s accepted a plan without composition evidence", policy)
			}
			hash, _, hashErr := hashio.File(output)
			if hashErr != nil {
				t.Fatal(hashErr)
			}
			if _, fetchErr := backend.Fetch(context.Background(), hash); fetchErr == nil {
				t.Fatalf("%s published bytes for a rejected artifact", policy)
			}
		})
	}
}

// TestStoreArtifactNoOverlayCertifyAllowsMissingSummary keeps the other
// direction honest: a plan with nothing to certify must not fail because an
// optional sidecar is absent.
func TestStoreArtifactNoOverlayCertifyAllowsMissingSummary(t *testing.T) {
	artifact, _ := storeArtifactWithPolicy(t, renderVerifyCertify, verificationSourceOnlyPlan(), false)
	if artifact.Verification == nil || artifact.Verification.Composition != queue.VerificationCompositionNotRequired {
		t.Fatalf("verification = %+v, want not_required", artifact.Verification)
	}
}
