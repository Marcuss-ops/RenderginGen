// batch_test.go locks the batch expansion contract: deterministic IDs,
// content-derived idempotency keys, fail-closed validation and the
// multilingual (Strategy A) base+overlay shape.
package batch

import (
	"encoding/json"
	"testing"

	queue "github.com/Marcuss-ops/RenderginGen/queue/client"
)

func flatManifest() Manifest {
	return Manifest{
		Schema:  SchemaBatchManifestV1,
		BatchID: "batch-2026-09-10",
		Jobs: []FlatJob{
			{
				ID:         "video-001",
				RenderPlan: json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p1"}`),
				Assets: []queue.AssetRef{
					{Hash: "h2", LogicalPath: "videos/base.mp4"},
					{Hash: "h1", LogicalPath: "fonts/Inter.ttf"},
				},
			},
			{
				ID:         "video-002",
				RenderPlan: json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p2"}`),
			},
		},
	}
}

func multilingualManifest() MultilingualManifest {
	return MultilingualManifest{
		Schema:  SchemaMultilingualBatchV1,
		BatchID: "ml-batch-1",
		Base: FlatJob{
			ID:         "base-001",
			RenderPlan: json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"base"}`),
			Assets:     []queue.AssetRef{{Hash: "bh", LogicalPath: "videos/base.mp4"}},
		},
		Langs: []LanguageJob{
			{
				Language:   "en",
				BaseJobID:  "base-001",
				RenderPlan: json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"en"}`),
				Assets:     []queue.AssetRef{{Hash: "eh", LogicalPath: "subtitles/en.ass"}},
			},
			{
				Language:   "it",
				BaseJobID:  "base-001",
				RenderPlan: json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"it"}`),
			},
		},
	}
}

func TestExpandFlatDeterministic(t *testing.T) {
	jobs, err := Expand(flatManifest())
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
	if jobs[0].ID != "batch-2026-09-10:video-001" {
		t.Fatalf("job id = %q, want batch-2026-09-10:video-001", jobs[0].ID)
	}
	if jobs[0].Schema != queue.JobSchemaV1 || jobs[0].Version != queue.JobSchemaVersionV1 {
		t.Fatalf("envelope = %s v%d, want %s v%d", jobs[0].Schema, jobs[0].Version, queue.JobSchemaV1, queue.JobSchemaVersionV1)
	}
	// Determinism: a second expansion produces byte-identical keys.
	again, _ := Expand(flatManifest())
	for i := range jobs {
		if jobs[i].IdempotencyKey != again[i].IdempotencyKey {
			t.Fatalf("idempotency key not deterministic for job %d", i)
		}
	}
}

func TestIdempotencyKeyChangesWithContent(t *testing.T) {
	m := flatManifest()
	first, _ := Expand(m)
	m.Jobs[0].RenderPlan = json.RawMessage(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p1-edited"}`)
	second, _ := Expand(m)
	if first[0].IdempotencyKey == second[0].IdempotencyKey {
		t.Fatal("editing a plan must produce a new idempotency key")
	}
	// Job ID stays stable: the replay targets the same logical video.
	if first[0].ID != second[0].ID {
		t.Fatalf("job id drifted: %q vs %q", first[0].ID, second[0].ID)
	}
}

func TestIdempotencyKeyIgnoresAssetOrder(t *testing.T) {
	m := flatManifest()
	first, _ := Expand(m)
	// Flip the asset list order; the canonical hash must not change.
	m.Jobs[0].Assets = []queue.AssetRef{
		{Hash: "h1", LogicalPath: "fonts/Inter.ttf"},
		{Hash: "h2", LogicalPath: "videos/base.mp4"},
	}
	second, _ := Expand(m)
	if first[0].IdempotencyKey != second[0].IdempotencyKey {
		t.Fatal("asset order must not affect the idempotency key")
	}
}

func TestExpandMultilingualShape(t *testing.T) {
	jobs, err := ExpandMultilingual(multilingualManifest())
	if err != nil {
		t.Fatalf("ExpandMultilingual: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs = %d, want 3 (1 base + 2 languages)", len(jobs))
	}
	base := jobs[0]
	if base.ID != "ml-batch-1:base-001" {
		t.Fatalf("base id = %q", base.ID)
	}
	if base.JobType != "" || base.ParentJobID != "" {
		t.Fatalf("base must be a plain render job, got type=%q parent=%q", base.JobType, base.ParentJobID)
	}
	en := jobs[1]
	if en.ID != "ml-batch-1:base-001.overlay.en" {
		t.Fatalf("en id = %q", en.ID)
	}
	if en.JobType != "overlay.render" {
		t.Fatalf("en job_type = %q, want overlay.render", en.JobType)
	}
	if en.ParentJobID != base.ID {
		t.Fatalf("en parent = %q, want %q", en.ParentJobID, base.ID)
	}
	if en.IdempotencyKey == base.IdempotencyKey {
		t.Fatal("base and language jobs must have distinct idempotency keys")
	}
}

func TestExpandValidationFailClosed(t *testing.T) {
	cases := []struct {
		name string
		fn   func() error
	}{
		{"wrong schema", func() error {
			m := flatManifest()
			m.Schema = "renderinggen.batch-manifest.v0"
			_, err := Expand(m)
			return err
		}},
		{"empty batch id", func() error {
			m := flatManifest()
			m.BatchID = ""
			_, err := Expand(m)
			return err
		}},
		{"no jobs", func() error {
			m := flatManifest()
			m.Jobs = nil
			_, err := Expand(m)
			return err
		}},
		{"empty job id", func() error {
			m := flatManifest()
			m.Jobs[0].ID = " "
			_, err := Expand(m)
			return err
		}},
		{"duplicate job id", func() error {
			m := flatManifest()
			m.Jobs[1].ID = m.Jobs[0].ID
			_, err := Expand(m)
			return err
		}},
		{"empty plan", func() error {
			m := flatManifest()
			m.Jobs[0].RenderPlan = nil
			_, err := Expand(m)
			return err
		}},
		{"plan not object", func() error {
			m := flatManifest()
			m.Jobs[0].RenderPlan = json.RawMessage(`[1,2]`)
			_, err := Expand(m)
			return err
		}},
		{"asset missing hash", func() error {
			m := flatManifest()
			m.Jobs[0].Assets = []queue.AssetRef{{LogicalPath: "x.mp4"}}
			_, err := Expand(m)
			return err
		}},
		{"duplicate asset path", func() error {
			m := flatManifest()
			m.Jobs[0].Assets = []queue.AssetRef{
				{Hash: "a", LogicalPath: "x.mp4"},
				{Hash: "b", LogicalPath: "x.mp4"},
			}
			_, err := Expand(m)
			return err
		}},
	}
	for _, tc := range cases {
		if err := tc.fn(); err == nil {
			t.Fatalf("%s: expected an error, got nil", tc.name)
		}
	}
}

func TestExpandMultilingualValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*MultilingualManifest)
	}{
		{"empty base id", func(m *MultilingualManifest) { m.Base.ID = "" }},
		{"duplicate language", func(m *MultilingualManifest) { m.Langs[1].Language = "en" }},
		{"empty language", func(m *MultilingualManifest) { m.Langs[0].Language = "" }},
		{"mismatched base_job_id", func(m *MultilingualManifest) { m.Langs[0].BaseJobID = "other-base" }},
		{"empty lang plan", func(m *MultilingualManifest) { m.Langs[0].RenderPlan = nil }},
	}
	for _, tc := range cases {
		m := multilingualManifest()
		tc.mut(&m)
		if _, err := ExpandMultilingual(m); err == nil {
			t.Fatalf("%s: expected an error, got nil", tc.name)
		}
	}
}

func TestDecodeAutoDetectsShape(t *testing.T) {
	flat, err := json.Marshal(flatManifest())
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := Decode(flat)
	if err != nil {
		t.Fatalf("Decode flat: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("flat decode = %d jobs", len(jobs))
	}
	ml, err := json.Marshal(multilingualManifest())
	if err != nil {
		t.Fatal(err)
	}
	jobs, err = Decode(ml)
	if err != nil {
		t.Fatalf("Decode multilingual: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("multilingual decode = %d jobs", len(jobs))
	}
	if _, err := Decode([]byte(`{"schema_version":"unknown.v9"}`)); err == nil {
		t.Fatal("unknown schema must be rejected")
	}
	if _, err := Decode([]byte(`{}`)); err == nil {
		t.Fatal("missing schema_version must be rejected")
	}
}
