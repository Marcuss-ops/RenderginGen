// conformance_baseline_test.go covers the RATCHET LEDGER and the scan SCOPE: the
// committed baseline parses, every entry names a known rule, a baselined
// occurrence that gets fixed must have its entry removed, a standalone checkout
// does not inherit a sibling's baseline, and the resolved scan scope is what the
// caller thinks it is.
//
// The ledger half is what makes the gate a ratchet instead of a blocklist: the
// baseline can only shrink, and every entry in it is a promise to remove a real
// violation rather than a place to park a new one.
package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
