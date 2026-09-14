package processor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

// processorDir resolves this package's source directory.
func processorDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Dir(source)
}

// moduleRoot resolves the renderinggen module root from this package's source
// directory (<module>/internal/processor -> <module>).
func moduleRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(processorDir(t), "..", "..")
}

// productionSources returns every non-test Go source of the MODULE, keyed by
// module-relative slash path.
//
// The scan is module-wide on purpose. It used to read only this package, which
// left the same class of typo unguarded one directory away: the artifact ledger
// mirror (internal/artifactdb) projects metric names into its own SQLite
// columns, and the CLI tools under cmd/ build metric maps for reports. A name
// written there with a typo is persisted just as permanently as one written
// here.
func productionSources(t *testing.T) map[string]string {
	t.Helper()
	root := moduleRoot(t)
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no production sources found; the vocabulary check would be vacuous")
	}
	if len(out) < 40 {
		t.Fatalf("only %d production sources found under %s; the module-wide walk is no longer covering the module", len(out), root)
	}
	return out
}

// metricConstValues parses internal/metricnames' own source and returns every
// string constant it declares, name -> value. The test needs the VALUES of the
// constants the pipeline references (metrics[metricnames.ProbeUS] carries no
// name of its own), and reading them from source keeps the vocabulary single-
// sourced: nothing in this test restates a metric name.
//
// It is also the only check that can catch the drift the vocabulary package
// exists to prevent: a constant declared but never registered in the package's
// `vocab` map compiles fine, yet Unit() then reports it as undeclared and the
// queue silently falls back to deriving the unit from the name suffix.
func metricConstValues(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(processorDir(t)), "metricnames")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read metricnames sources: %v", err)
	}
	fset := token.NewFileSet()
	out := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Values) != len(value.Names) {
					continue
				}
				for i, ident := range value.Names {
					lit, ok := value.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					unquoted, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					out[ident.Name] = unquoted
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no string constants found in internal/metricnames; the vocabulary check would be vacuous")
	}
	return out
}

// TestEveryEmittedMetricIsDeclared is the completeness half of the metric
// vocabulary: every metric name written on a metrics map by the production
// pipeline must exist in internal/metricnames. Before this, a metric name was
// an unconstrained string literal duplicated across the pipeline, the artifact
// ledger, the SQLite mirror and the queue's processing_metrics rows — and the
// queue persists whatever it is given, deriving the unit from the suffix, so a
// typo became a permanently mislabelled row instead of a failure.
//
// Both spellings are checked:
//
//	metrics["literal"]              the literal must be declared;
//	metrics[metricnames.SomeName]   the constant's VALUE must be declared,
//	                               which is what catches a constant that was
//	                               added to the package but not to its vocab.
func TestEveryEmittedMetricIsDeclared(t *testing.T) {
	literalRe := regexp.MustCompile(`(?:metrics|Metrics|phaseMetrics)\["([a-z0-9_]+)"\]`)
	constRe := regexp.MustCompile(`(?:metrics|Metrics|phaseMetrics)\[metricnames\.([A-Za-z0-9_]+)\]`)
	consts := metricConstValues(t)

	var undeclared []string
	checked, refs := 0, 0
	for name, source := range productionSources(t) {
		for _, match := range literalRe.FindAllStringSubmatch(source, -1) {
			checked++
			if !metricnames.Declared(match[1]) {
				undeclared = append(undeclared, name+": "+match[1])
			}
		}
		for _, match := range constRe.FindAllStringSubmatch(source, -1) {
			checked++
			value, ok := consts[match[1]]
			if !ok {
				t.Errorf("%s: metrics[metricnames.%s] does not name a string constant declared by internal/metricnames", name, match[1])
				continue
			}
			refs++
			if !metricnames.Declared(value) {
				undeclared = append(undeclared, name+": metricnames."+match[1]+" ("+value+")")
			}
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		t.Fatalf("undeclared metric names emitted by the processor (add them to internal/metricnames):\n  %s", strings.Join(undeclared, "\n  "))
	}
	// A regex (or a parser) that silently stops matching would turn this check
	// into a no-op; the emitters are numerous, so a small floor keeps it honest.
	if checked < 10 {
		t.Fatalf("only %d metric-map assignments matched; the vocabulary check has gone vacuous", checked)
	}
	if refs == 0 {
		t.Fatal("no metricnames.<Constant> reference was resolved against the declared constants; the constant half of the check is vacuous")
	}
}

// TestPhaseStemsHaveBothUnits is the dynamic half of the same rule: the staged
// pipeline writes `<stem>_us` and `<stem>_ms` from ONE stem, which no literal
// scan can see. Every stem the pipeline passes around must therefore declare
// both spellings, or a phase would be recorded under a name the vocabulary (and
// the queue's unit derivation) does not know.
func TestPhaseStemsHaveBothUnits(t *testing.T) {
	stemCall := regexp.MustCompile(`record(?:Phase)?\((?:"([a-z0-9_]+)"|metricnames\.([A-Za-z0-9]+))\s*,`)
	byConstant := map[string]string{
		"AssetMaterializeStem":    metricnames.AssetMaterializeStem,
		"PlanStem":                metricnames.PlanStem,
		"RenderStem":              metricnames.RenderStem,
		"PublishStem":             metricnames.PublishStem,
		"ProbeStem":               metricnames.ProbeStem,
		"OverlayCompileStem":      metricnames.OverlayCompileStem,
		"SubtitleBurnStem":        metricnames.SubtitleBurnStem,
		"SHA256Stem":              metricnames.SHA256Stem,
		"ObjectStoreUploadStem":   metricnames.ObjectStoreUploadStem,
		"PrepareTotalStem":        metricnames.PrepareTotalStem,
		"PrepareMaterializeStem":  metricnames.PrepareMaterializeStem,
		"PrepareSceneCompileStem": metricnames.PrepareSceneCompileStem,
		"PrepareAssetResolveStem": metricnames.PrepareAssetResolveStem,
		"PrepareBurnStem":         metricnames.PrepareBurnStem,
		"PrepareMarshalStem":      metricnames.PrepareMarshalStem,
		"PreparePrefetchStem":     metricnames.PreparePrefetchStem,
	}
	checked := 0
	for name, source := range productionSources(t) {
		for _, match := range stemCall.FindAllStringSubmatch(source, -1) {
			stem := match[1]
			if stem == "" {
				stem = byConstant[match[2]]
				if stem == "" {
					t.Errorf("%s uses metricnames.%s as a phase stem; this test does not know that constant — declare its pair", name, match[2])
					continue
				}
			}
			checked++
			if !metricnames.StemHasPair(stem) {
				t.Errorf("%s records phase %q, but %s_us/%s_ms are not both declared", name, stem, stem, stem)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no phase-stem call site found; the stem check would be vacuous")
	}
	if !metricnames.StemHasPair(metricnames.AssetMaterializeStem) {
		t.Fatal("the asset_materialize phase pair must stay declared (the ledger's column is projected from it)")
	}
}
