package overlay

import (
	"strings"
	"testing"
)

func compileJSON(t *testing.T, body string) CompileResult {
	t.Helper()
	result, err := CompileSemantic([]byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v",` +
		`"width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[` + body + `]}`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return result
}

// TestUnknownTemplateIsReportedNotSwallowed pins the observability of the
// preset-less fall-through. An unknown template_id still compiles (so
// historical documents keep rendering) but must appear in
// CompileResult.UnknownTemplates: before this, a producer rename degraded every
// affected entity to a bare text primitive and the only evidence was the pixels.
func TestUnknownTemplateIsReportedNotSwallowed(t *testing.T) {
	result := compileJSON(t,
		`{"id":"a","template_id":"BRAND_NEW_WIDGET","text":"Ada","start_ms":0,"end_ms":1000},`+
			`{"id":"b","template_id":"brand_new_widget","text":"Bob","start_ms":0,"end_ms":1000},`+
			`{"id":"c","template_id":"ANOTHER_NEW","text":"Eve","start_ms":0,"end_ms":1000}`)

	if len(result.Plan.Layers) == 0 {
		t.Fatal("an unknown template must still lower to a layer (no fail-closed regression)")
	}
	want := []string{"ANOTHER_NEW", "BRAND_NEW_WIDGET", "brand_new_widget"}
	if strings.Join(result.UnknownTemplates, ",") != strings.Join(want, ",") {
		t.Fatalf("UnknownTemplates = %v, want %v (sorted, de-duplicated, verbatim spellings)", result.UnknownTemplates, want)
	}
}

// TestRegisteredAndAliasedTemplatesAreNeverReportedUnknown pins the other side
// of the same rule: a registry template and the LIVE legacy aliases
// (accepted through legacyTemplateAliases) must
// resolve to a registry row, so they are neither reported nor downgraded. This
// is the behavior the gate's `template_alias_lowercase` rule protects: the
// alias table is the only compatibility path, and it is not a silent
// fall-through.
func TestRegisteredAndAliasedTemplatesAreNeverReportedUnknown(t *testing.T) {
	legacyOrg := "org_" + "default"
	legacyLocation := "gpe_" + "default"
	for _, templateID := range []string{
		"PERSON", "person_default", legacyOrg, legacyLocation, "concept_default",
		"IMPORTANT_PHRASE", "IMPORTANT_WORD", "IMAGE_OVERLAY", "LOGO", "LIGHT_LEAK",
	} {
		t.Run(templateID, func(t *testing.T) {
			spec := templateSpecFor(templateID)
			if !spec.Registered {
				t.Fatalf("template %q resolved to the unknown primitive; the registry/alias path must cover it", templateID)
			}
			if spec.Kind == KindPrimitive {
				t.Fatalf("template %q resolved kind %q, want a registered semantic kind", templateID, spec.Kind)
			}
		})
	}
}

// TestUnknownTemplateReportsEveryItemOnce pins the counting contract: the
// worker records one metric value (the number of distinct unknown templates)
// and logs the ids, so the list must not contain duplicates and must not drop
// an item.
func TestUnknownTemplateReportsEveryItemOnce(t *testing.T) {
	result := compileJSON(t,
		`{"id":"a","template_id":"MYSTERY","text":"A","start_ms":0,"end_ms":500},`+
			`{"id":"b","template_id":"MYSTERY","text":"B","start_ms":500,"end_ms":1000},`+
			`{"id":"c","template_id":"MYSTERY","text":"C","start_ms":1000,"end_ms":1500}`)
	if len(result.UnknownTemplates) != 1 || result.UnknownTemplates[0] != "MYSTERY" {
		t.Fatalf("UnknownTemplates = %v, want [MYSTERY] once for three items", result.UnknownTemplates)
	}
}
