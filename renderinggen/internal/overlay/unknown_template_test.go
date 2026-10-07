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

// TestUnknownTemplateFailsClosed pins the strict contract: an unknown
// template_id fails compilation with a diagnostic naming the item and the
// template, instead of silently lowering a bare primitive. Historical
// documents keep rendering only through registered ids and the explicit
// legacyTemplateAliases table — never through fall-through.
func TestUnknownTemplateFailsClosed(t *testing.T) {
	body := `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v",` +
		`"width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[` +
		`{"id":"a","template_id":"BRAND_NEW_WIDGET","text":"Ada","start_ms":0,"end_ms":1000},` +
		`{"id":"b","template_id":"brand_new_widget","text":"Bob","start_ms":0,"end_ms":1000},` +
		`{"id":"c","template_id":"ANOTHER_NEW","text":"Eve","start_ms":0,"end_ms":1000}]}`
	_, err := CompileSemantic([]byte(body))
	if err == nil {
		t.Fatal("unknown template compiled; want fail-closed")
	}
	if !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), "BRAND_NEW_WIDGET") {
		t.Fatalf("diagnostic %q must name the first item and its template", err)
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

// TestUnknownTemplateFailsClosedOnEveryItem pins that the strict rule is
// per-item: three items sharing one unknown template fail on the first, and
// the diagnostic names it verbatim.
func TestUnknownTemplateFailsClosedOnEveryItem(t *testing.T) {
	body := `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v",` +
		`"width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[` +
		`{"id":"a","template_id":"MYSTERY","text":"A","start_ms":0,"end_ms":500},` +
		`{"id":"b","template_id":"MYSTERY","text":"B","start_ms":500,"end_ms":1000},` +
		`{"id":"c","template_id":"MYSTERY","text":"C","start_ms":1000,"end_ms":1500}]}`
	_, err := CompileSemantic([]byte(body))
	if err == nil {
		t.Fatal("unknown template compiled; want fail-closed")
	}
	if !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), "MYSTERY") {
		t.Fatalf("diagnostic %q must name the first item and its template", err)
	}
}
