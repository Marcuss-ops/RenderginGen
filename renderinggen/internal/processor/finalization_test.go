package processor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

type finalizerQueue struct {
	children  []*queue.Job
	claimed   bool
	completed *queue.Artifact
	// claimResult, when set, is the claimed parent job returned by the claim
	// (carrying the anchor's RenderPlan — the audio-aware assembly input).
	claimResult *queue.Job
	// events records the protocol order — every child-family read and the
	// claim — so a test can assert how many reads the completion path costs and
	// that the claim sits between them.
	events []string
}

func (q *finalizerQueue) Children(context.Context, string) ([]*queue.Job, error) {
	q.events = append(q.events, "children")
	return q.children, nil
}
func (q *finalizerQueue) ClaimFinalization(context.Context, string) (*queue.Job, bool, error) {
	q.events = append(q.events, "claim")
	if q.claimed {
		return nil, false, nil
	}
	q.claimed = true
	if q.claimResult != nil {
		return q.claimResult, true, nil
	}
	return &queue.Job{ID: "parent", Lease: time.Hour}, true, nil
}
func (q *finalizerQueue) Renew(context.Context, string) error { return nil }
func (q *finalizerQueue) Complete(_ context.Context, _ string, artifact queue.Artifact) error {
	q.completed = &artifact
	return nil
}

// copySafeChunkArtifact mirrors what the production lane certifies on a
// completed chunk: the structural probe ran (a job carrying a FrameRange is
// always probed) and the copy-safety facts are proven.
func copySafeChunkArtifact(storageKey string) *queue.Artifact {
	return &queue.Artifact{StorageKey: storageKey, CopyEligible: true, ClosedGOP: true, FirstFrameKeyframe: true}
}

type finalizerAssembler struct{}

func (finalizerAssembler) Assemble(_ context.Context, req chronon.AssembleRequest) error {
	out, err := os.Create(req.Output)
	if err != nil {
		return err
	}
	defer out.Close()
	for _, input := range req.Inputs {
		in, err := os.Open(input)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			return err
		}
		in.Close()
	}
	return nil
}

func TestParentFinalizerStoresPublishesAndCompletes(t *testing.T) {
	ctx := context.Background()
	store := storage.New(storage.NewMemory(), storage.Options{L2Dir: t.TempDir()})
	if err := store.Put(ctx, "chunk-a", []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "chunk-b", []byte("B")); err != nil {
		t.Fatal(err)
	}
	children := []*queue.Job{
		{ID: "c0", ChunkIndex: 0, FrameRange: &queue.FrameRange{Start: 0, End: 10}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-a")},
		{ID: "c1", ChunkIndex: 1, FrameRange: &queue.FrameRange{Start: 10, End: 20}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-b")},
	}
	q := &finalizerQueue{children: children}
	driveDir := t.TempDir()
	f := NewParentFinalizer(q, store, finalizerAssembler{}, drive.NewMock(driveDir, 0), "worker", t.TempDir())
	finalized, artifact, err := f.Finalize(ctx, "parent", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !finalized || q.completed == nil {
		t.Fatalf("finalized=%t completed=%+v", finalized, q.completed)
	}
	if artifact.StorageKey == "" || artifact.DriveFileID == "" || artifact.SizeBytes != 2 {
		t.Fatalf("artifact=%+v", artifact)
	}
	if _, err := os.Stat(filepath.Join(driveDir, artifact.ArtifactHash)); err != nil {
		t.Fatalf("published output missing: %v", err)
	}
}

// TestFinalizeFromChildrenDerivesRangeAndReadsOnceBeforeTheClaim pins the
// completion-path contract the worker relies on. The worker used to read the
// child family itself just to learn first/last frame, then hand the finalizer
// bounds it immediately re-read — one duplicate Children() round-trip per
// completed chunk. FinalizeFromChildren derives the range from the same read it
// validates, so the protocol is exactly children → claim → children.
//
// A third read means the duplicate round-trip is back; a derived range that
// disagrees with the family's own span would assemble the wrong window.
func TestFinalizeFromChildrenDerivesRangeAndReadsOnceBeforeTheClaim(t *testing.T) {
	ctx := context.Background()
	store := storage.New(storage.NewMemory(), storage.Options{L2Dir: t.TempDir()})
	if err := store.Put(ctx, "chunk-a", []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "chunk-b", []byte("B")); err != nil {
		t.Fatal(err)
	}
	children := []*queue.Job{
		{ID: "c0", ChunkIndex: 0, FrameRange: &queue.FrameRange{Start: 0, End: 10}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-a")},
		{ID: "c1", ChunkIndex: 1, FrameRange: &queue.FrameRange{Start: 10, End: 20}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-b")},
	}
	q := &finalizerQueue{children: children}
	f := NewParentFinalizer(q, store, finalizerAssembler{}, nil, "worker", t.TempDir())

	finalized, _, err := f.FinalizeFromChildren(ctx, "parent")
	if err != nil {
		t.Fatalf("finalize from children: %v", err)
	}
	if !finalized || q.completed == nil {
		t.Fatalf("finalized=%t completed=%+v", finalized, q.completed)
	}
	want := []string{"children", "claim", "children"}
	if strings.Join(q.events, ",") != strings.Join(want, ",") {
		t.Fatalf("protocol = %v, want %v (a third read is the duplicate round-trip this entry point exists to remove)", q.events, want)
	}
	// The derived range must be the family's span: assembling [0,20) is only
	// correct if start/end came from children[0].Start and children[last].End.
	if q.completed.SizeBytes != 2 {
		t.Fatalf("assembled artifact = %+v, want both chunks concatenated", q.completed)
	}
}

// TestFinalizeFromChildrenRejectsAFamilyWithPartialRanges pins fail-closed
// behavior: a partially ranged child family is not a normal run-correlation
// family and must not be assembled or claimed.
func TestFinalizeFromChildrenRejectsAFamilyWithPartialRanges(t *testing.T) {
	cases := map[string][]*queue.Job{
		"mixed ranged and unranged children": {
			{ID: "c0", ChunkIndex: 0, FrameRange: &queue.FrameRange{Start: 0, End: 10}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-a")},
			{ID: "c1", ChunkIndex: 1, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-b")},
		},
	}
	for name, children := range cases {
		t.Run(name, func(t *testing.T) {
			q := &finalizerQueue{children: children}
			f := NewParentFinalizer(q, nil, finalizerAssembler{}, nil, "worker", t.TempDir())
			finalized, _, err := f.FinalizeFromChildren(context.Background(), "parent")
			if err == nil || finalized {
				t.Fatalf("finalized=%t err=%v, want a refusal", finalized, err)
			}
			for _, event := range q.events {
				if event == "claim" {
					t.Fatalf("the parent must not be claimed for a family that cannot be assembled: %v", q.events)
				}
			}
		})
	}
}

func TestFinalizeFromChildrenIgnoresUnrangedCorrelationChildren(t *testing.T) {
	q := &finalizerQueue{children: []*queue.Job{
		{ID: "ordinary-render-child", ChunkIndex: 0, State: queue.StateCompleted, Artifact: &queue.Artifact{StorageKey: "artifact"}},
	}}
	f := NewParentFinalizer(q, nil, nil, nil, "worker", t.TempDir())
	finalized, _, err := f.FinalizeFromChildren(context.Background(), "pipeline-run-parent")
	if err != nil || finalized {
		t.Fatalf("unranged run-correlation family: finalized=%t err=%v, want not-applicable without error", finalized, err)
	}
	if q.claimed {
		t.Fatal("ordinary correlated render jobs must not claim the parent finalization lease")
	}
}

// recordingAssembler captures the assembly request while still materialising
// the output file the artifact probe reads afterwards.
type recordingAssembler struct {
	finalizerAssembler
	got []chronon.AssembleRequest
}

func (r *recordingAssembler) Assemble(ctx context.Context, req chronon.AssembleRequest) error {
	r.got = append(r.got, req)
	return r.finalizerAssembler.Assemble(ctx, req)
}

// anchorPlanJSON builds the anchor's semantic plan document — the FULL
// mapping (audio block + source asset), which is what the chunk producer
// submits for the anchor while children carry the video-only mapping.
func anchorPlanJSON(sourceHash string, withAudio bool) json.RawMessage {
	audio := ""
	if withAudio {
		audio = `,"audio":{"mode":"copy_if_compatible","codec":"aac","sample_rate":48000,"channels":2}`
	}
	return json.RawMessage(fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chunk-parent","video_id":"source-asset-001","width":1920,"height":1080,"fps_num":24,"fps_den":1,"output_profile_id":"VELOX_ASSEMBLY_READY_V1","duration_ms":2000,"source":{"asset_id":"source-asset-001","path":"assets/semantic/source-asset-001/source.mp4","sha256":%q},"items":[]%s}`,
		sourceHash, audio))
}

func audioAwareFamily(t *testing.T, withAudio bool) (*finalizerQueue, *storage.Client, []byte, string) {
	t.Helper()
	ctx := context.Background()
	store := storage.New(storage.NewMemory(), storage.Options{L2Dir: t.TempDir()})
	sourceBytes := []byte("source-media-with-audio-stream")
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(sourceBytes))
	if err := store.Put(ctx, sourceHash, sourceBytes); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"chunk-a", "chunk-b"} {
		if err := store.Put(ctx, key, []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	children := []*queue.Job{
		{ID: "c0", ChunkIndex: 0, FrameRange: &queue.FrameRange{Start: 0, End: 10}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-a")},
		{ID: "c1", ChunkIndex: 1, FrameRange: &queue.FrameRange{Start: 10, End: 20}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-b")},
	}
	q := &finalizerQueue{children: children, claimResult: &queue.Job{
		ID: "parent", Lease: time.Hour, RenderPlan: anchorPlanJSON(sourceHash, withAudio),
	}}
	return q, store, sourceBytes, sourceHash
}

// TestParentFinalizerMuxesSourceAudioOnce pins the audio-aware assembly
// contract: the family anchor is the SINGLE audio owner. Children are
// video-only (silent segments), the anchor's plan document carries the audio
// block + source asset, so the finalizer resolves the source through the
// store and hands the assembler exactly ONE audio mux request over the
// concatenated video. The parent keeps the classic MP4 because it carries
// audio.
func TestParentFinalizerMuxesSourceAudioOnce(t *testing.T) {
	q, store, sourceBytes, _ := audioAwareFamily(t, true)
	rec := &recordingAssembler{}
	f := NewParentFinalizer(q, store, rec, nil, "worker", t.TempDir())
	finalized, _, err := f.Finalize(context.Background(), "parent", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !finalized {
		t.Fatal("family must finalize")
	}
	if len(rec.got) != 1 {
		t.Fatalf("assembler calls = %d, want 1 (the source stream is muxed exactly once)", len(rec.got))
	}
	req := rec.got[0]
	if req.AudioSourcePath == "" {
		t.Fatal("anchor must mux the source audio over the assembled video")
	}
	if req.AudioTargetSampleRate != 48000 {
		t.Fatalf("audio target sample rate = %d, want 48000 (the plan's declared rate)", req.AudioTargetSampleRate)
	}
	if req.FragmentedMP4 {
		t.Fatal("an audio-bearing parent keeps the classic MP4")
	}
	if data, err := os.ReadFile(req.AudioSourcePath); err != nil || !bytes.Equal(data, sourceBytes) {
		t.Fatalf("audio source must be the hash-verified store materialisation: %v", err)
	}
}

// TestParentFinalizerSilentAnchorStaysFragmented pins the no-audio lane: a
// plan document with no audio block leaves the family silent and fragmented
// (bytes final when appended), with no audio mux request at all.
func TestParentFinalizerSilentAnchorStaysFragmented(t *testing.T) {
	q, store, _, _ := audioAwareFamily(t, false)
	rec := &recordingAssembler{}
	f := NewParentFinalizer(q, store, rec, nil, "worker", t.TempDir())
	finalized, _, err := f.Finalize(context.Background(), "parent", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !finalized {
		t.Fatal("family must finalize")
	}
	if len(rec.got) != 1 {
		t.Fatalf("assembler calls = %d, want 1", len(rec.got))
	}
	req := rec.got[0]
	if req.AudioSourcePath != "" || req.AudioTargetSampleRate != 0 {
		t.Fatalf("silent family must carry no audio mux request: %+v", req)
	}
	if !req.FragmentedMP4 {
		t.Fatal("a silent parent is assembled as a fragmented MP4")
	}
}
