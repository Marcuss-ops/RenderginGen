package artifactdb

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
) // snakeCase converts a Go field name (with acronym runs and digit suffixes)
// to the column naming convention used by the mirror's schema. It follows the
// same rule the field names were originally written by: JobID -> job_id,
// FPSNum -> fps_num, ChrononTimingURL -> chronon_timing_url,
// ChrononTimingSHA256 -> chronon_timing_sha256, DriveUploadUS ->
// drive_upload_us.
func snakeCase(field string) string {
	runes := []rune(field)
	var out []rune
	for i, r := range runes {
		if r < 'A' || r > 'Z' {
			out = append(out, r)
			continue
		}
		if i > 0 {
			prevLowerOrDigit := (runes[i-1] >= 'a' && runes[i-1] <= 'z') || (runes[i-1] >= '0' && runes[i-1] <= '9')
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			prevUpper := runes[i-1] >= 'A' && runes[i-1] <= 'Z'
			if prevLowerOrDigit || (prevUpper && nextLower) {
				out = append(out, '_')
			}
		}
		out = append(out, rune(r-'A'+'a'))
	}
	return string(out)
}

// columnOverrides are the fields whose COLUMN name is not a mechanical
// snake_case of the field name. They are stated once, here, instead of being
// implied independently by three hand-written lists (CREATE TABLE, the INSERT
// column list and the upsert SET list).
var columnOverrides = map[string]string{
	"ImportantPhraseCnt":  "important_phrase_count",
	"ImportantWordCnt":    "important_word_count",
	"ObjectStoreUploadUS": "objectstore_upload_us",
}

// TestMirrorSchemaCoversEveryRecordField pins the mirror's declared schema
// against the record it persists. The mirror is a PROJECTION of the artifact
// fact set whose owner is the queue's render_artifacts row; before this test,
// the projection existed as three hand-maintained parallel lists (the Go
// struct, the CREATE TABLE columns and the upsert column/SET lists) that
// nothing compared, so a new fact had to be added in every one of them from
// memory.
func TestMirrorSchemaCoversEveryRecordField(t *testing.T) {
	schemaColumns := map[string]bool{}
	for _, line := range strings.Split(schema, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "CREATE TABLE") || trimmed == ");" || trimmed == "(" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		schemaColumns[fields[0]] = true
	}

	typ := reflect.TypeOf(ArtifactRecord{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i).Name
		column := columnOverrides[field]
		if column == "" {
			column = snakeCase(field)
		}
		if !schemaColumns[column] {
			t.Errorf("ArtifactRecord.%s maps to column %q, which the mirror schema does not declare", field, column)
		}
		delete(schemaColumns, column)
	}
	if len(schemaColumns) > 0 {
		left := make([]string, 0, len(schemaColumns))
		for column := range schemaColumns {
			left = append(left, column)
		}
		sort.Strings(left)
		t.Errorf("the mirror schema declares columns no record field fills: %v", left)
	}
}

// TestMirrorUpsertListsEveryColumn pins the second and third parallel lists
// (the INSERT column list and the DO UPDATE SET list) to the same column set,
// so a column added to the schema cannot silently miss the upsert.
func TestMirrorUpsertListsEveryColumn(t *testing.T) {
	schemaColumns := []string{}
	for _, line := range strings.Split(schema, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "CREATE TABLE") || trimmed == ");" || trimmed == "(" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) > 0 {
			schemaColumns = append(schemaColumns, fields[0])
		}
	}
	sort.Strings(schemaColumns)

	insertBlock := upsert[strings.Index(upsert, "INSERT INTO artifact_records ("):strings.Index(upsert, ") VALUES")]
	insertColumns := []string{}
	for _, part := range strings.Split(strings.TrimPrefix(insertBlock, "INSERT INTO artifact_records ("), ",") {
		insertColumns = append(insertColumns, strings.TrimSpace(part))
	}
	sort.Strings(insertColumns)
	if strings.Join(insertColumns, ",") != strings.Join(schemaColumns, ",") {
		t.Fatalf("upsert INSERT columns %v != schema columns %v", insertColumns, schemaColumns)
	}

	setBlock := upsert[strings.Index(upsert, "DO UPDATE SET"):]
	setRe := regexp.MustCompile(`(?m)^\s*([a-z_0-9]+)\s*=\s*excluded\.`)
	setColumns := map[string]bool{}
	for _, match := range setRe.FindAllStringSubmatch(setBlock, -1) {
		setColumns[match[1]] = true
		if !containsString(schemaColumns, match[1]) {
			t.Errorf("upsert SET updates column %q, which the schema does not declare", match[1])
		}
	}
	// A column may legitimately be absent from the SET list only when the
	// INSERT already fixes it: the primary key, and created_at (the row's first
	// write wins for the upsert's identity).
	mutable := []string{"job_id", "created_at"}
	for _, column := range schemaColumns {
		if setColumns[column] || containsString(mutable, column) {
			continue
		}
		t.Errorf("column %q is declared and inserted but never updated by the upsert SET list (a re-record would silently keep the stale value)", column)
	}
}

// TestMirrorMetricKeysAreTheSharedVocabulary pins the mirror's "DB metrics"
// projection to internal/metricnames: the mirror must not invent metric names
// of its own, because the same names are what the queue persists in
// processing_metrics.
func TestMirrorMetricKeysAreTheSharedVocabulary(t *testing.T) {
	keys := ArtifactRecord{}.Metrics()
	if len(keys) == 0 {
		t.Fatal("the mirror projects no metric keys; the check would be vacuous")
	}
	for key := range keys {
		if !metricnames.Declared(key) {
			t.Errorf("mirror metric key %q is not part of the shared vocabulary", key)
		}
		if !containsAny([]string{
			metricnames.OverlayCompileUS, metricnames.AssetMaterializeUS,
		}, key) {
			continue
		}
	}
	// The ledger-critical timings must be projected, or the mirror would report
	// a render with no phase breakdown.
	for _, want := range []string{
		metricnames.OverlayCompileUS, metricnames.AssetMaterializeUS,
		metricnames.ChrononRenderUS, metricnames.SHA256US,
		metricnames.ObjectStoreUploadUS, metricnames.DriveUploadUS, metricnames.TotalUS,
	} {
		if _, ok := keys[want]; !ok {
			t.Errorf("mirror does not project the ledger timing %q", want)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func containsAny(list []string, want string) bool { return containsString(list, want) }
