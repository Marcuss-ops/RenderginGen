package processor

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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

// productionSources returns the package's non-test Go files.
func productionSources(t *testing.T) map[string]string {
	t.Helper()
	dir := processorDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(raw)
	}
	if len(out) == 0 {
		t.Fatal("no production sources found; the vocabulary check would be vacuous")
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
func TestEveryEmittedMetricIsDeclared(t *testing.T) {
	literal := regexp.MustCompile(`(?:metrics|Metrics|phaseMetrics)\["([a-z0-9_]+)"\]`)
	var undeclared []string
	checked := 0
	for name, source := range productionSources(t) {
		for _, match := range literal.FindAllStringSubmatch(source, -1) {
			checked++
			if !metricnames.Declared(match[1]) {
				undeclared = append(undeclared, name+": "+match[1])
			}
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		t.Fatalf("undeclared metric names emitted by the processor (add them to internal/metricnames):\n  %s", strings.Join(undeclared, "\n  "))
	}
	// A regex that silently stops matching would turn this check into a
	// no-op; the emitters are numerous, so a small floor keeps it honest.
	if checked < 10 {
		t.Fatalf("only %d metric-map assignments matched; the vocabulary check has gone vacuous", checked)
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
		"AssetMaterializeStem":  metricnames.AssetMaterializeStem,
		"PlanStem":              metricnames.PlanStem,
		"RenderStem":            metricnames.RenderStem,
		"PublishStem":           metricnames.PublishStem,
		"ProbeStem":             metricnames.ProbeStem,
		"OverlayCompileStem":    metricnames.OverlayCompileStem,
		"SubtitleBurnStem":      metricnames.SubtitleBurnStem,
		"SHA256Stem":            metricnames.SHA256Stem,
		"ObjectStoreUploadStem": metricnames.ObjectStoreUploadStem,
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
