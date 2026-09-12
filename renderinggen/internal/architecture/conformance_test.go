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

	newViolations, stale := base.Split(vs, AbsentSibling)

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
// silently broken regex can never turn the gate into a no-op. The exemplars
// below are the LIVE shapes the rules are meant to catch, not the identifiers
// they historically named.
func TestRulesDetectEveryMarker(t *testing.T) {
	cases := []struct {
		rule string
		rel  string
		line string
	}{
		{"render_plan_v1_schema", "x/foo.go", `schema := "chronon.render-plan.v1"`},
		{"render_plan_unversioned_schema", "x/foo.go", `const s = "chronon.render-plan"`},
		{"module_path_typo", "x/go.mod", "module github.com/Marcuss-ops/RenderginGen/queue"},
		{"hardcoded_home_path", "x/foo_test.go", `bin := "/home/dev/bin/chronon"`},
		{"template_alias_org_default", "x/foo.go", `case "ORG_DEFAULT":`},
		{"template_alias_gpe_default", "x/foo.go", `case "GPE_DEFAULT":`},
		// The live shapes, not the deleted symbol names these rules used to
		// match: the old patterns could never fire in any repository.
		{"entity_template_inference", "renderinggen/internal/overlay/foo.go", `if item.Template == "PERSON" {`},
		{"semantic_stats_second_pass", "renderinggen/internal/overlay/foo.go", `s := Stats{EntityCount: 1}`},
		{"template_alias_lowercase", "renderinggen/internal/overlay/foo.go", `if templateID == "org_default" {`},
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

// TestRetargetedRulesRespectTheirExclusions pins that the ownership
// exclusions of the three retargeted rules are exact: the canonical
// declaration sites stay legal, everything else fires. Without this, a rewrite
// of the rules could quietly exempt the whole package again.
func TestRetargetedRulesRespectTheirExclusions(t *testing.T) {
	legal := []struct {
		name string
		path string
		line string
	}{
		{"alias table owner", "renderinggen/internal/overlay/registry.go", `"org_default": "ORGANIZATION_DEFAULT",`},
		{"alias test owner", "renderinggen/internal/overlay/registry_test.go", `{"gpe_default", KindLocation},`},
		{"counters owner", "renderinggen/internal/overlay/stats.go", `s := Stats{EntityCount: 1}`},
		{"compile pass owner", "renderinggen/internal/overlay/semantic_compile.go", `return nil, nil, Stats{}, err`},
		{"required-field check", "renderinggen/internal/overlay/semantic_compile.go", `if item.Template == "" {`},
	}
	for _, tc := range legal {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			full := filepath.Join(dir, filepath.FromSlash(tc.path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("package x\n"+tc.line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := ScanTargets([]Target{{Dir: dir}})
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range got {
				switch v.Rule {
				case "template_alias_lowercase", "semantic_stats_second_pass", "entity_template_inference":
					t.Errorf("%s: %s fired on the canonical owner (%s)", tc.name, v.Rule, tc.line)
				}
			}
		})
	}
}

// TestRuleExtensionsAreScanned is the data-driven coherence check between the
// scanner's file-kind list and the rules' selectors. A rule extension the
// scanner never reads is an unreachable selector — the historical
// `.dockerfile` entry, while every *.Dockerfile silently went unread — so the
// two lists must be the same set.
func TestRuleExtensionsAreScanned(t *testing.T) {
	for _, r := range Rules() {
		for _, ext := range r.exts {
			if !scannedExts[ext] && !scannedNames[ext] {
				t.Errorf("rule %q selects extension %q, which scannedExts/scannedNames never reads (unreachable selector)", r.id, ext)
			}
		}
	}
}

// TestDockerfileCarriersAreScanned pins the fix for that dead selector:
// `*.Dockerfile` files are read by the gate (the bare name "Dockerfile" was
// the only carrier covered before).
func TestDockerfileCarriersAreScanned(t *testing.T) {
	for _, name := range []string{"renderinggen-worker.Dockerfile", "Dockerfile"} {
		if !scannedFile(name) {
			t.Errorf("%s is not scanned by the gate", name)
		}
	}
	dir := t.TempDir()
	carrier := filepath.Join(dir, "worker.Dockerfile")
	if err := os.WriteFile(carrier, []byte("RUN /home/dev/bin/chronon3d_cli --help\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ScanTargets([]Target{{Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range got {
		if v.Rule == "hardcoded_home_path" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hardcoded_home_path did not fire on a *.Dockerfile carrier: %+v", got)
	}
}

// TestPartnnFilenameRule proves the filename rule fires without reading bytes,
// for EVERY scanned carrier. The rule used to be scoped to .go, which let a
// live `run-golden-overlay_part02.sh` in the E2E gate pass untouched while the
// documentation claimed the rule covered the whole workspace.
func TestPartnnFilenameRule(t *testing.T) {
	for _, name := range []string{"client_part01.go", "run-golden-overlay_part02.sh"} {
		dir := t.TempDir()
		full := filepath.Join(dir, name)
		body := "package client\n"
		if strings.HasSuffix(name, ".sh") {
			body = "#!/usr/bin/env bash\n"
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ScanTargets([]Target{{Dir: dir}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, v := range got {
			if v.Rule == "partnn_filename" && v.File == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("partnn_filename did not fire for %s: %+v", name, got)
		}
	}
}

// TestRulesCatchEscapedCarriers is the regression for the evasion that made
// the gate lie: a shell script embedding a JSON document escapes its quotes,
// so a rule matching `"chronon.render-plan"` saw `\"chronon.render-plan\"`
// and stayed silent while the live smoke test submitted the forbidden
// unversioned schema. Rules now match the unescaped projection of each line.
func TestRulesCatchEscapedCarriers(t *testing.T) {
	cases := []struct {
		name string
		file string
		line string
	}{
		{"escaped quotes in a shell script", "x/run.sh", `  -d '{"id":"j","render_plan":{"schema": "chronon.render-plan", "version":1}}'`},
		{"escaped v1 in yaml", "x/cfg.yaml", `plan: "chronon.render-plan.v1"`},
		{"home path in yaml", "x/cfg.yaml", `home: /home/ci/src/Chronon3d/build`},
		{"macOS home path in yaml", "x/cfg.yaml", `home: /Users/dev/src/Chronon3d/build`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			full := filepath.Join(dir, filepath.FromSlash(tc.file))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(tc.line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := ScanTargets([]Target{{Dir: dir}})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 {
				t.Fatalf("no rule fired for %q", tc.line)
			}
		})
	}
}

// TestExemptionAllowlistsAreLive is the ghost-entry check the audit asks for:
// every exemption the gate carries must still refer to something real. A
// selfSkip/docExemptions path that no longer exists means the marker carrier
// was moved or renamed and is now NOT exempt — the gate would fire on the new
// path, or worse, a category skip silently covers it. A rule exclusion that
// matches no scanned file protects nothing.
//
// scannedExts is deliberately exempt from the failure rule: it is a selector,
// not an exemption (it widens what the gate reads, it never hides a finding),
// and several entries legitimately exist for sibling-only carriers. Zero-carrier
// selectors are reported as a log, so the ledger is still visible.
func TestExemptionAllowlistsAreLive(t *testing.T) {
	root := RepoRoot()

	for path := range selfSkip {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Errorf("selfSkip entry %q does not resolve to a file; the marker carrier moved and is no longer skipped", path)
		}
	}
	for path := range docExemptions {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Errorf("docExemptions entry %q does not resolve to a file; delete the stale exemption", path)
		}
	}

	// One walk of the repo collects the scanned paths, so each exclusion can be
	// tested against the carrier set it is supposed to protect.
	scanned := make([]string, 0, 4096)
	carriers := map[string]bool{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		carriers[relSlash] = true
		if scannedFile(d.Name()) {
			scanned = append(scanned, relSlash)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(scanned) == 0 {
		t.Fatal("the exemption walk found no scanned files; the check below is vacuous")
	}

	for _, r := range Rules() {
		for _, ex := range r.exclude {
			matched := false
			for _, path := range scanned {
				if strings.Contains(path, ex) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("rule %q excludes %q, which matches no scanned file — a ghost exclusion that protects nothing", r.id, ex)
			}
		}
	}

	// scannedExts is a selector, not an exemption: zero-carrier entries are
	// reported, never failed, because several exist for sibling-only carriers.
	carrierExts := map[string]bool{}
	for _, target := range Targets() {
		_ = filepath.WalkDir(target.Dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			carrierExts[strings.ToLower(filepath.Ext(path))] = true
			return nil
		})
	}
	for ext := range scannedExts {
		if !carrierExts[ext] {
			t.Logf("scannedExts carries %q, which no file in the scanned workspace uses (harmless selector; delete only with the rule that names it)", ext)
		}
	}
}

// TestSkipDirsDoNotShadowSourcePackages pins the false-enforcement fix: an
// ambiguous generated-output name (artifacts, tmp, out, results, secrets) is
// only skipped at a target root. PipelineGen ships LIVE source packages at
// internal/capabilities/assets/artifacts/ and internal/platform/sqlite/artifacts/;
// matching the basename at any depth silently un-scanned them, so a forbidden
// marker added there would never have been reported.
func TestSkipDirsDoNotShadowSourcePackages(t *testing.T) {
	cases := []struct {
		name   string
		rel    string
		marker bool // true = the rule MUST fire, false = the tree is skipped
	}{
		{"nested source package named artifacts is scanned", "refactored/internal/capabilities/assets/artifacts/x.go", true},
		{"nested source package named results is scanned", "refactored/tests/operational/results/x.go", true},
		{"root-level generated artifacts is skipped", "artifacts/x.go", false},
		{"root-level generated out is skipped", "out/x.go", false},
		{"nested staging .tmp is skipped at any depth", "renderinggen/.tmp/demolition/x.go", false},
		{"node_modules is skipped at any depth", "renderinggen/internal/node_modules/x.go", false},
		{"build- prefix is skipped at any depth", "rust/target/debug/build-local/x.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			full := filepath.Join(dir, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			// render_plan_unversioned_schema is not rootOnly, so it fires on a
			// `refactored/`-prefixed synthetic path too.
			if err := os.WriteFile(full, []byte("package x\n\nconst s = \"chronon.render-plan\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			vs, err := ScanTargets([]Target{{Dir: dir}})
			if err != nil {
				t.Fatal(err)
			}
			fired := false
			for _, v := range vs {
				if v.Rule == "render_plan_unversioned_schema" {
					fired = true
				}
			}
			if fired != tc.marker {
				t.Errorf("%s: rule fired = %v, want %v (the skip scope for %q is wrong)", tc.rel, fired, tc.marker, filepath.Base(filepath.Dir(tc.rel)))
			}
		})
	}
}

// TestExemptionsActuallySuppress proves each exemption MECHANISM works, not
// merely that its table has entries: an exemption that no longer suppresses
// would turn a deliberate carve-out into a surprise failure, and one that
// suppresses too much would hide real violations.
func TestExemptionsActuallySuppress(t *testing.T) {
	write := func(t *testing.T, rel, body string) string {
		t.Helper()
		dir := t.TempDir()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	fired := func(t *testing.T, dir, rule string) bool {
		t.Helper()
		vs, err := ScanTargets([]Target{{Dir: dir}})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vs {
			if v.Rule == rule {
				return true
			}
		}
		return false
	}

	marker := "home := /home/dev/bin/chronon3d_cli\n"

	t.Run("docExemptions suppresses only CONFORMANCE.md", func(t *testing.T) {
		if fired(t, write(t, "CONFORMANCE.md", marker), "hardcoded_home_path") {
			t.Error("docExemptions no longer exempts CONFORMANCE.md")
		}
		if !fired(t, write(t, "OTHER.md", marker), "hardcoded_home_path") {
			t.Error("a non-exempt doc must still fail the rule")
		}
	})

	t.Run("selfSkip suppresses only the gate's own carriers", func(t *testing.T) {
		if fired(t, write(t, "renderinggen/internal/architecture/conformance.go", marker), "hardcoded_home_path") {
			t.Error("selfSkip no longer exempts the rule table")
		}
		if !fired(t, write(t, "renderinggen/internal/architecture/other.go", marker), "hardcoded_home_path") {
			t.Error("a non-exempt architecture file must still fail the rule")
		}
	})

	t.Run("skipDirs and build-* suppress generated trees", func(t *testing.T) {
		if fired(t, write(t, "node_modules/p.js", marker), "hardcoded_home_path") {
			t.Error("skipDirs no longer skips node_modules")
		}
		if fired(t, write(t, "build-local/gen.go", marker), "hardcoded_home_path") {
			t.Error("the build-* prefix rule no longer skips generated build trees")
		}
		if !fired(t, write(t, "src/main.go", marker), "hardcoded_home_path") {
			t.Error("a normal source tree must still fail the rule")
		}
	})

	t.Run("rule exclude carves out only the declared owner", func(t *testing.T) {
		body := "package overlay\nvar _ = Stats{}\n"
		if fired(t, write(t, "renderinggen/internal/overlay/stats.go", body), "semantic_stats_second_pass") {
			t.Error("semantic_stats_second_pass no longer exempts its owner stats.go")
		}
		if !fired(t, write(t, "renderinggen/internal/overlay/other.go", body), "semantic_stats_second_pass") {
			t.Error("a non-owner overlay file must still fail semantic_stats_second_pass")
		}
	})
}

// TestConformanceDocListsEveryRule turns the rule table in CONFORMANCE.md from
// an independent copy of the rule vocabulary into a checked projection: the
// gate's rule ids and the documented ids must be the same set, in both
// directions. A renamed or deleted rule that leaves the doc behind (or a
// documented rule that no longer exists) fails here instead of misleading the
// next reader.
func TestConformanceDocListsEveryRule(t *testing.T) {
	docPath := filepath.Join(RepoRoot(), "CONFORMANCE.md")
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read CONFORMANCE.md: %v", err)
	}
	documented := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "| `") {
			continue
		}
		rest := trimmed[len("| `"):]
		end := strings.Index(rest, "`")
		if end <= 0 {
			continue
		}
		documented[rest[:end]] = true
	}

	known := map[string]bool{}
	for _, r := range Rules() {
		known[r.id] = true
		if !documented[r.id] {
			t.Errorf("rule %q is implemented but not listed in CONFORMANCE.md", r.id)
		}
	}
	for id := range documented {
		if !known[id] {
			t.Errorf("CONFORMANCE.md documents rule %q, which no longer exists", id)
		}
	}
}

// TestBaselineRulesAreKnown pins the ledger to the rule set: a baseline entry
// whose rule was renamed or deleted can never match a violation again, so it
// would sit in the ledger forever without this check.
func TestBaselineRulesAreKnown(t *testing.T) {
	raw, err := os.ReadFile(baselinePath())
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	known := map[string]bool{}
	for _, r := range Rules() {
		known[r.id] = true
	}
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		rule, _, ok := strings.Cut(trimmed, "|")
		if !ok {
			t.Errorf("baseline:%d: entry must be rule|file", i+1)
			continue
		}
		if !known[rule] {
			t.Errorf("baseline:%d: rule %q is not implemented (immortal ledger line)", i+1, rule)
		}
	}
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
