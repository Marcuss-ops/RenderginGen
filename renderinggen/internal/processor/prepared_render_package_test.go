package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

func TestPreparedRenderPackageIsContentAddressedAndReused(t *testing.T) {
	ctx := context.Background()
	proc, store, _ := newProcessorWith(t, Options{})
	if err := store.Put(ctx, videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("seed source asset: %v", err)
	}
	prepareJob := validJob()
	prepareJob.ID = "family-prepare"
	prepareJob.JobType = queue.JobTypeRenderPrepare

	artifact, err := proc.PrepareRenderPackage(ctx, prepareJob)
	if err != nil {
		t.Fatalf("prepare immutable package: %v", err)
	}
	if artifact.Kind != "prepared_render_package" || artifact.ArtifactHash == "" || artifact.StorageKey != artifact.ArtifactHash {
		t.Fatalf("prepared package artifact = %+v", artifact)
	}

	child := validJob()
	child.ID = "family-chunk-0"
	child.JobType = queue.JobTypeRenderSegment
	var indentedPlan bytes.Buffer
	if err := json.Indent(&indentedPlan, child.RenderPlan, "", "  "); err != nil {
		t.Fatalf("indent equivalent source plan: %v", err)
	}
	child.RenderPlan = indentedPlan.Bytes()
	child.PreparedRenderPackage = &queue.PreparedRenderPackageRef{
		PackageID: artifact.ArtifactHash, PlanSHA256: preparedRenderPlanSHA256(child.RenderPlan),
		AssetManifestSHA: preparedRenderAssetManifestSHA(child.Assets),
		CompilerVersion:  artifact.ChrononVersion, ProfileID: artifact.ProfileID,
	}
	child.FrameRange = &queue.FrameRange{Start: 0, End: 30}

	prepared, err := proc.PrepareJob(ctx, child)
	if err != nil {
		t.Fatalf("prepare chunk from immutable package: %v", err)
	}
	defer func() { _ = prepared.Workspace.Cleanup() }()

	rawPackage, err := store.Get(ctx, artifact.ArtifactHash)
	if err != nil {
		t.Fatalf("fetch package object: %v", err)
	}
	if got := storage.Hash(rawPackage); got != artifact.ArtifactHash {
		t.Fatalf("package storage hash = %s, want %s", got, artifact.ArtifactHash)
	}
	var pkg PreparedRenderPackageV1
	if err := json.Unmarshal(rawPackage, &pkg); err != nil {
		t.Fatalf("decode stored package: %v", err)
	}
	if err := pkg.Validate(); err != nil {
		t.Fatalf("validate stored package: %v", err)
	}
	compiledPlan, err := prepared.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal reused plan: %v", err)
	}
	if string(compiledPlan) != string(pkg.Plan) {
		t.Fatalf("chunk plan differs from immutable prepared plan")
	}
	if !reflect.DeepEqual(prepared.Stats, pkg.Stats) {
		t.Fatalf("chunk stats = %+v, package stats = %+v", prepared.Stats, pkg.Stats)
	}
}

func TestPreparedRenderPlanIdentityIsCanonical(t *testing.T) {
	first := json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1","width":1280,"height":720}`)
	second := json.RawMessage(`{ "height": 720, "width": 1280, "schema_version": "renderinggen.overlay-plan.v1" }`)
	if got, want := preparedRenderPlanSHA256(first), preparedRenderPlanSHA256(second); got == "" || got != want {
		t.Fatalf("canonical plan digests differ: %q != %q", got, want)
	}
}

func TestPreparedRenderPackageRejectsRequestIdentityDrift(t *testing.T) {
	ctx := context.Background()
	proc, store, _ := newProcessorWith(t, Options{})
	if err := store.Put(ctx, videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("seed source asset: %v", err)
	}
	prepareJob := validJob()
	prepareJob.ID = "identity-prepare"
	prepareJob.JobType = queue.JobTypeRenderPrepare
	artifact, err := proc.PrepareRenderPackage(ctx, prepareJob)
	if err != nil {
		t.Fatalf("prepare immutable package: %v", err)
	}

	child := validJob()
	child.ID = "identity-chunk"
	child.JobType = queue.JobTypeRenderSegment
	child.RenderPlan = bytes.Replace(child.RenderPlan, []byte("video-983"), []byte("video-984"), 1)
	child.PreparedRenderPackage = &queue.PreparedRenderPackageRef{
		PackageID: artifact.ArtifactHash, PlanSHA256: preparedRenderPlanSHA256(prepareJob.RenderPlan),
		AssetManifestSHA: preparedRenderAssetManifestSHA(child.Assets),
		CompilerVersion:  artifact.ChrononVersion, ProfileID: artifact.ProfileID,
	}
	if _, err := proc.PrepareJob(ctx, child); err == nil {
		t.Fatal("PrepareJob accepted a package whose source request identity drifted")
	}
}
