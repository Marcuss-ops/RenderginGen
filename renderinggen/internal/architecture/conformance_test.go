package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baselinePath is the committed ratchet ledger.
func baselinePath() string {
	return filepath.Join(PackageDir(), "testdata", "conformance-baseline.txt")
}

// TestArchitectureConformance is the permanent cross-repo boundary gate. It
// fails when a forbidden marker reappears anywhere in the workspace, or when a
// baselined occurrence is fixed but its ledger entry is not removed.
func TestArchitectureConformance(t *testing.T) {
	vs, err := ScanAll()
	if err != nil {
		t.Fatalf("scan %v: %v", RepoRoot(), err)
	}

	base, err := LoadBaseline(baselinePath())
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}

	if os.Getenv("UPDATE_CONFORMANCE_BASELINE") == "1" {
		if err := base.Write(vs); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		t.Logf("conformance baseline rewritten with %d entries", len(vs))
		return
	}

	newViolations, stale := base.Split(vs, PathExists)

	if len(newViolations) > 0 {
		var sb strings.Builder
		sb.WriteString("architecture conformance: NEW forbidden markers introduced.\n")
		sb.WriteString("PipelineGen owns semantic intent; RenderingGen owns visual lowering; Chronon owns execution.\n")
		sb.WriteString("Do not reintroduce the deleted contract — route through the canonical one.\n\n")
		for _, v := range newViolations {
			sb.WriteString("  " + v.Format() + "\n")
			if v.Snippet != "" {
				sb.WriteString("      > " + v.Snippet + "\n")
			}
		}
		t.Errorf("%s", sb.String())
	}

	if len(stale) > 0 {
		t.Errorf("architecture conformance: %d stale baseline entries — the violation is fixed, remove the ledger line (ratchet down):\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// TestRulesDetectEveryMarker proves the gate actually fires for each rule, so a
// silently broken regex can never turn the gate into a no-op.
func TestRulesDetectEveryMarker(t *testing.T) {
	cases := []struct {
		rule string
		rel  string
		line string
	}{
		{"render_plan_v1_schema", "x/foo.go", `schema := "chronon.render-plan.v1"`},
		{"render_plan_unversioned_schema", "x/foo.go", `const s = "chronon.render-plan"`},
		{"module_path_typo", "x/go.mod", "module github.com/Marcuss-ops/RenderginGen/queue"},
		{"hardcoded_home_path", "x/foo_test.go", `bin := "/home/pierone/bin/chronon"`},
		{"template_alias_org_default", "x/foo.go", `case "ORG_DEFAULT":`},
		{"template_alias_gpe_default", "x/foo.go", `case "GPE_DEFAULT":`},
		{"entity_template_inference", "x/foo.go", `if isEntityTemplate(t) {`},
		{"semantic_stats_second_pass", "x/foo.go", `stats, err := SemanticStats(raw)`},
		{"legacy_layer_preset_field", "x/chronon/plan.go", "Preset string `json:\"preset\"`"},
	}

	for _, tc := range cases {
		dir := t.TempDir()
		full := filepath.Join(dir, filepath.FromSlash(tc.rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(tc.line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ScanTargets([]Target{{Dir: dir}})
		if err != nil {
			t.Fatalf("%s: scan: %v", tc.rule, err)
		}
		found := false
		for _, v := range got {
			if v.Rule == tc.rule {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("rule %q did not fire for %q", tc.rule, tc.line)
		}
	}
}

// TestPartnnFilenameRule proves the filename rule fires without reading bytes.
func TestPartnnFilenameRule(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "client_part01.go")
	if err := os.WriteFile(full, []byte("package client\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ScanTargets([]Target{{Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		if v.Rule == "partnn_filename" && v.File == "client_part01.go" {
			return
		}
	}
	t.Fatalf("partnn_filename did not fire: %+v", got)
}

// TestRepoRootContainsModule pins the root discovery used by ScanAll.
func TestRepoRootContainsModule(t *testing.T) {
	root := RepoRoot()
	if _, err := os.Stat(filepath.Join(root, "renderinggen", "go.mod")); err != nil {
		t.Fatalf("RepoRoot()=%q does not contain renderinggen/go.mod: %v", root, err)
	}
}

// TestStandaloneRepoIgnoresSiblingBaseline pins that a RenderingGen-only
// checkout (CONFORMANCE_ROOT scoped to the repo) never scans or staleness-fails
// on sibling baselines it cannot see.
func TestStandaloneRepoIgnoresSiblingBaseline(t *testing.T) {
	t.Setenv("CONFORMANCE_ROOT", RepoRoot())
	if PathExists("refactored/does/not/exist.go") {
		t.Fatal("sibling file must not resolve in standalone mode")
	}
	vs, err := ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if strings.HasPrefix(v.File, "refactored/") || strings.HasPrefix(v.File, "Chronon3d/") {
			t.Fatalf("standalone scan must not include sibling path %q", v.File)
		}
	}
}
