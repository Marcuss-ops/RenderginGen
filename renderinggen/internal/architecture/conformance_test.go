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

// TestResolvedScanScopeIsExplicit pins WHAT the gate actually covers, because
// that coverage is a function of the checkout and therefore differs between the
// full workspace and CI. The scope was previously implicit: the package comment
// described the sibling case, nothing asserted it, and CI (which checks out this
// repository alone) reported green while every cross-repo rule was a no-op.
//
// The assertions are:
//
//  1. target 0 is this repository, prefix-less — the scope always includes the
//     tree the gate ships in;
//  2. every other target carries `<sibling>/` as its prefix, names a declared
//     sibling repo, and exists on disk — so a rule can rely on the prefix;
//  3. when a sibling IS present, every cross-repo rule has a real carrier
//     inside it. A sibling scan that finds no applicable file is vacuous: the
//     rule exists but can never fire. When no sibling is present
//     the same fact is logged instead of failed, because the standalone
//     checkout is a legitimate configuration (see CONFORMANCE.md, "Enforcement
//     scope").
func TestResolvedScanScopeIsExplicit(t *testing.T) {
	t.Setenv("CONFORMANCE_ROOT", "") // exercise the real Targets() resolution
	targets := Targets()
	if len(targets) == 0 {
		t.Fatal("Targets() returned nothing; the gate has no scan scope")
	}
	if targets[0].Dir != RepoRoot() || targets[0].Prefix != "" {
		t.Fatalf("targets[0] = %+v, want the repository root %q with no prefix", targets[0], RepoRoot())
	}

	siblings := targets[1:]
	if len(siblings) > len(siblingRepos) {
		t.Fatalf("expected at most %d sibling targets, got %d", len(siblingRepos), len(siblings))
	}
	for _, target := range siblings {
		name := strings.TrimSuffix(target.Prefix, "/")
		if target.Prefix != name+"/" {
			t.Errorf("sibling target %+v must carry a <name>/ prefix", target)
		}
		declared := false
		for _, known := range siblingRepos {
			if name == known {
				declared = true
			}
		}
		if !declared {
			t.Errorf("target prefix %q is not in siblingRepos %v; a rule could not name it deliberately", target.Prefix, siblingRepos)
		}
		if st, err := os.Stat(target.Dir); err != nil || !st.IsDir() {
			t.Errorf("sibling target %q is not a directory: %v", target.Dir, err)
		}
	}

	// Cross-repo rules are the ones the sibling scan is FOR. Count real carriers
	// per rule across the sibling trees. Repo-hygiene (rootOnly) and
	// package-scoped (nodes) rules are deliberately excluded: they describe this
	// repository's own layout and cannot apply to a sibling.
	crossRepoCarriers := map[string]int{}
	rules := Rules() // hoisted: Rules() recompiles every regex
	for _, target := range siblings {
		_ = filepath.WalkDir(target.Dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(target.Dir, path)
			if relErr != nil {
				return nil
			}
			relSlash := filepath.ToSlash(rel)
			if d.IsDir() {
				if relSlash == "." {
					return nil
				}
				if skipDir(relSlash, d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil || info.Size() > maxFileBytes || !scannedFile(d.Name()) {
				return nil
			}
			prefixed := target.Prefix + relSlash
			for _, r := range rules {
				if !crossRepo(r) || !r.applies(prefixed) {
					continue
				}
				crossRepoCarriers[r.id]++
			}
			return nil
		})
	}

	if len(siblings) == 0 {
		t.Logf("standalone scope: no sibling repository checked out, so every cross-repo rule is UNEXERCISED here (this is the CI configuration; see CONFORMANCE.md \"Enforcement scope\")")
		return
	}

	unenforced := make([]string, 0, len(rules))
	for _, r := range rules {
		if !crossRepo(r) {
			continue
		}
		if crossRepoCarriers[r.id] == 0 {
			unenforced = append(unenforced, r.id)
		}
	}
	if len(unenforced) == 0 {
		t.Logf("full-workspace scope: every cross-repo rule has carriers in %d sibling target(s)", len(siblings))
	}
	if len(unenforced) > 0 {
		t.Errorf("sibling trees are checked out but these cross-repo rules have no carrier file there, so they can never fire in the full workspace: %v", unenforced)
	}
}

// crossRepo reports whether a rule can fire on a sibling-repository path, which
// is the only reason the gate scans the siblings at all. A `rootOnly` rule is
// this repository's own hygiene (a sibling's hygiene belongs to the sibling's
// gate), and a rule with `nodes` carries its own path selector instead of
// targeting the sibling prefix. Everything else must be able to fire on a
// `refactored/`/`Chronon3d/` carrier.
func crossRepo(r rule) bool {
	return !r.rootOnly && len(r.nodes) == 0 && r.textRe != nil
}

// TestCrossRepoRulesAreSiblingScoped pins the scope of every rule against the
// table CONFORMANCE.md publishes, in both directions: a rule that silently
// narrows itself to this repository (or one that forgets the sibling scope)
// fails here, and so does a rule whose scope moved without the doc following.
// It is the reason "Enforcement scope" is a checked statement, not a claim.
func TestCrossRepoRulesAreSiblingScoped(t *testing.T) {
	// wantScope is the audited classification of every rule id.
	wantScope := map[string]string{
		// This repository's own hygiene: never applied to a sibling tree.
		"hardcoded_home_path":      "rootOnly",
		"template_alias_lowercase": "rootOnly",
		"partnn_filename":          "rootOnly",
		// Scoped to a RenderingGen package by `nodes`.
		"entity_template_inference":  "packageLocal",
		"semantic_stats_second_pass": "packageLocal",
		"legacy_layer_preset_field":  "packageLocal",
		// The boundary violations this repository's CI cannot see on its own.
		"render_plan_v1_schema":          "crossRepo",
		"render_plan_unversioned_schema": "crossRepo",
		"module_path_typo":               "crossRepo",
		"template_alias_org_default":     "crossRepo",
		"template_alias_gpe_default":     "crossRepo",
	}
	for _, r := range Rules() {
		want, known := wantScope[r.id]
		if !known {
			t.Errorf("rule %q is not classified in the scope table; decide whether it is rootOnly, packageLocal or crossRepo and document it", r.id)
			continue
		}
		switch want {
		case "rootOnly":
			if !r.rootOnly {
				t.Errorf("rule %q is documented as rootOnly but is not configured that way", r.id)
			}
		case "packageLocal":
			if r.rootOnly || len(r.nodes) == 0 {
				t.Errorf("rule %q is documented as packageLocal but has rootOnly=%v nodes=%v", r.id, r.rootOnly, r.nodes)
			}
		case "crossRepo":
			if !crossRepo(r) {
				t.Errorf("rule %q is documented as crossRepo but is scoped away from sibling trees (rootOnly=%v nodes=%v)", r.id, r.rootOnly, r.nodes)
				continue
			}
			for _, carrier := range []string{"refactored/internal/x.go", "Chronon3d/src/x.cpp"} {
				if !r.applies(carrier) {
					t.Errorf("cross-repo rule %q does not apply to %s; the sibling scan cannot enforce it", r.id, carrier)
				}
			}
		}
	}
	for id := range wantScope {
		found := false
		for _, r := range Rules() {
			if r.id == id {
				found = true
			}
		}
		if !found {
			t.Errorf("the scope table classifies %q, which no longer exists; delete the line", id)
		}
	}
}

// TestCIRunsRaceEnabledModuleTests pins the CI half of the concurrency story:
// every module job must run its tests with `-race -count=1`. The renderer's
// Progress callback is invoked from two output-streaming goroutines and the
// worker records that observation on the job, so an unsynchronised write is a
// shipped data race that a plain `go test` cannot see; a cached PASS is what
// `-count=1` removes. Both flags were added to catch exactly that, and nothing
// but this test keeps them from being dropped again.
//
// The one deliberate exception is internal/architecture in the renderinggen
// job: it is a pure file scanner, so instrumentation has nothing to observe and
// costs minutes on its regexp pass. It is excluded from the race run and
// executed un-instrumented in its own step — the assertion below demands BOTH
// halves, so dropping the gate or silently widening the exclusion fails here.
//
// Both module jobs must also disable the real-engine runtime certification
// suite (RENDERINGGEN_SKIP_GPU_E2E=1). A unit job must never depend on a binary
// happening to be absent: the day someone adds a Chronon checkout to the
// workspace, `go test ./...` would start rendering real MP4s on the runner.
func TestCIRunsRaceEnabledModuleTests(t *testing.T) {
	path := filepath.Join(RepoRoot(), ".github", "workflows", "build.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	workflow := string(raw)
	jobs := map[string]string{ // job name -> expected working directory
		"test-renderinggen": "renderinggen",
		"test-queue":        "queue",
		"test-objectstore":  "objectstore",
	}
	for job, dir := range jobs {
		block := ciJobBlock(t, workflow, job)
		if strings.TrimSpace(block) == "" {
			t.Errorf("CI job %q is gone; the module it gates is untested", job)
			continue
		}
		if !strings.Contains(block, "working-directory: "+dir) {
			t.Errorf("CI job %q no longer runs in %s/", job, dir)
		}
		if job == "test-renderinggen" {
			// The scanner is excluded from the instrumented run and must be run
			// separately, un-instrumented.
			if !strings.Contains(block, "go test -race -count=1 $(go list ./... | grep -v '/internal/architecture$')") {
				t.Errorf("CI job %q must run the concurrency-bearing packages under `-race -count=1`, excluding only internal/architecture", job)
			}
			if !strings.Contains(block, "go test -count=1 ./internal/architecture/...") {
				t.Errorf("CI job %q excludes internal/architecture from -race, so it must still run the gate in its own step", job)
			}
			if !strings.Contains(block, "RENDERINGGEN_SKIP_GPU_E2E=1") {
				t.Errorf("CI job %q must disable the real-engine runtime certification suite explicitly, so the unit job cannot start real renders", job)
			}
			continue
		}
		if !strings.Contains(block, "go test -race -count=1 ./...") {
			t.Errorf("CI job %q must run `go test -race -count=1 ./...`", job)
		}
	}
}

// TestCIRuntimeCertificationHasAnOwner pins the third leg of the test-tier
// decision. The unit jobs disable the real-engine certification suite
// (RENDERINGGEN_SKIP_GPU_E2E=1), which is correct — but it also means that
// without this job the suite that actually renders frames would have no owner
// anywhere: it would run only where a developer happened to have an engine, and
// nothing would say so. The job must do three things, all asserted here:
//
//  1. invoke the certification tests;
//  2. NOT set the skip variable (a job that sets it and "runs" the suite is the
//     false green this test exists to prevent);
//  3. be manual (workflow_dispatch) on a GPU runner, because the hosted runner
//     has no device and an unguarded job would fail on every push.
func TestCIRuntimeCertificationHasAnOwner(t *testing.T) {
	path := filepath.Join(RepoRoot(), ".github", "workflows", "build.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	workflow := string(raw)

	job := "runtime-certification"
	if !strings.Contains(workflow, "\n  "+job+":") {
		t.Fatalf("CI no longer declares the %q job: the unit jobs disable the runtime certification, so nothing would run it", job)
	}
	block := ciJobBlock(t, workflow, job)
	if !strings.Contains(block, "./internal/overlay/") {
		t.Errorf("CI job %q must run the certification suite under ./internal/overlay/", job)
	}
	if strings.Contains(block, "RENDERINGGEN_SKIP_GPU_E2E") {
		t.Errorf("CI job %q sets RENDERINGGEN_SKIP_GPU_E2E, so it would report a green run in which every certification test skipped", job)
	}
	if !strings.Contains(block, "CHRONON_BIN") {
		t.Errorf("CI job %q must point CHRONON_BIN at the engine it certifies", job)
	}
	if !strings.Contains(block, "workflow_dispatch") {
		t.Errorf("CI job %q must be manual: the hosted runners have no GPU", job)
	}
	if !strings.Contains(block, "self-hosted") {
		t.Errorf("CI job %q must select a GPU runner", job)
	}
}

// ciJobBlock slices one CI job out of a workflow by indentation. A job header is
// a line indented exactly two spaces ending in ':'; its body is everything up to
// the next line at the same indent depth (or a top-level key). Slicing on the raw
// text instead matched the first "\n  " of the four-space body indent and
// returned an empty block.
func ciJobBlock(t *testing.T, workflow, job string) string {
	t.Helper()
	var sb strings.Builder
	in := false
	for _, line := range strings.Split(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == line && trimmed != "" {
			in = false // top-level key ends any job body
			continue
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			in = trimmed == job+":"
			continue
		}
		if in {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// TestCIChecksOutNoSiblingRepository pins the other half of the scope table in
// CONFORMANCE.md: the workflow checks out THIS repository only, so cross-repo
// rules are not exercised in CI. The golden canary legitimately clones
// Chronon3d for the runtime image, but into a temporary directory — Targets()
// only looks beside the checkout, so that clone must never land in the
// workspace. If a sibling checkout is ever added here, the scope table and this
// test must change together, which is the point: the limitation is recorded,
// not discovered.
func TestCIChecksOutNoSiblingRepository(t *testing.T) {
	path := filepath.Join(RepoRoot(), ".github", "workflows", "build.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "repository:") {
			t.Errorf("build.yaml:%d adds a `repository:` input (%s); a sibling checkout changes the gate's enforced scope and CONFORMANCE.md", i+1, strings.TrimSpace(line))
		}
		if !strings.Contains(line, "clone") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		dest := fields[len(fields)-1]
		if strings.HasPrefix(dest, "/tmp/") || strings.Contains(dest, "runner.temp") {
			continue
		}
		if strings.Contains(strings.ToLower(dest), "chronon") || strings.Contains(strings.ToLower(dest), "refactored") {
			t.Errorf("build.yaml:%d clones a sibling into %q, which Targets() would scan: cross-repo enforcement is no longer local-only", i+1, dest)
		}
	}
}
