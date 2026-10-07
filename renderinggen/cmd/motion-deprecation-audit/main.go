// Command motion-deprecation-audit validates a proposal and reports consumers
// without modifying the canonical catalog or registry. It is intentionally an
// audit gate, not a command that marks or removes a production motion.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/schemaval"
)

const proposalSchema = "contracts/motion-deprecation-proposal.v1.schema.json"
const reportSchemaPath = "contracts/motion-deprecation-audit-report.v1.schema.json"
const reportSchema = "renderinggen.motion-deprecation-audit.v1"

// proposal mirrors the closed JSON Schema in contracts/.
type proposal struct {
	Schema                  string   `json:"schema"`
	Version                 int      `json:"version"`
	MotionID                string   `json:"motion_id"`
	DeprecatedOn            string   `json:"deprecated_on"`
	RemoveAfter             string   `json:"remove_after"`
	Reason                  string   `json:"reason"`
	ReplacementID           string   `json:"replacement_id"`
	RequiredTargets         []string `json:"required_targets"`
	Owner                   string   `json:"owner"`
	CompatibilityWindowDays int      `json:"compatibility_window_days,omitempty"`
	Consumers               []string `json:"consumers,omitempty"`
}

type report struct {
	Schema          string `json:"schema"`
	Version         int    `json:"version"`
	MotionID        string `json:"motion_id"`
	ReplacementID   string `json:"replacement_id"`
	Owner           string `json:"owner"`
	DeprecatedOn    string `json:"deprecated_on"`
	RemoveAfter     string `json:"remove_after"`
	AuditDate       string `json:"audit_date"`
	RegistryPresent bool   `json:"registry_present"`
	Selectable      bool   `json:"selectable_before_deprecation"`

	ReplacementPresent  bool     `json:"replacement_present"`
	TargetsCompatible   bool     `json:"targets_compatible"`
	Consumers           []string `json:"consumers"`
	DeclaredConsumers   []string `json:"declared_consumers"`
	UndeclaredConsumers []string `json:"undeclared_consumers"`
	MissingConsumers    []string `json:"missing_declared_consumers"`
	WindowDays          int      `json:"compatibility_window_days"`
	Gate                string   `json:"gate"`
	Limitations         []string `json:"limitations"`
}

func main() {
	proposalPath := flag.String("proposal", "", "versioned proposal JSON (required)")
	rootFlag := flag.String("root", "..", "repository root, relative to the RenderingGen Go module")
	outPath := flag.String("output", "", "write deterministic JSON report to this path (default stdout)")
	auditDateFlag := flag.String("audit-date", "", "audit date YYYY-MM-DD (default current UTC date; fixed date recommended for reproducibility)")
	flag.Parse()
	if strings.TrimSpace(*proposalPath) == "" {
		fatal("-proposal is required")
	}
	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		fatal("repository root: %v", err)
	}
	proposalFile := resolve(root, *proposalPath)
	raw, err := os.ReadFile(proposalFile)
	if err != nil {
		fatal("read proposal: %v", err)
	}
	schemaFile := filepath.Join(root, proposalSchema)
	if err := schemaval.ValidateFile(raw, schemaFile); err != nil {
		fatal("proposal schema: %v", err)
	}
	var p proposal
	if err := json.Unmarshal(raw, &p); err != nil {
		fatal("decode proposal: %v", err)
	}
	deprecatedOn, err := time.Parse("2006-01-02", p.DeprecatedOn)
	if err != nil {
		fatal("deprecated_on must be YYYY-MM-DD: %v", err)
	}
	removeAfter, err := time.Parse("2006-01-02", p.RemoveAfter)
	if err != nil {
		fatal("remove_after must be YYYY-MM-DD: %v", err)
	}
	auditDate := time.Now().UTC().Truncate(24 * time.Hour)
	if *auditDateFlag != "" {
		auditDate, err = time.Parse("2006-01-02", *auditDateFlag)
		if err != nil {
			fatal("audit-date must be YYYY-MM-DD: %v", err)
		}
	}
	window := p.CompatibilityWindowDays
	if window == 0 {
		window = int(removeAfter.Sub(deprecatedOn).Hours() / 24)
	}
	if removeAfter.Before(deprecatedOn) || window < 30 || int(removeAfter.Sub(deprecatedOn).Hours()/24) < window {
		fatal("removal date must be at least compatibility_window_days (%d, minimum 30) after deprecation", window)
	}
	files := discoverTextFiles(root)
	consumers := findConsumers(root, files, p.MotionID, proposalFile, resolve(root, *outPath))
	definition, err := resolveDefinition(p.MotionID)
	if err != nil {
		fatal("motion_id: %v", err)
	}
	if !motion.Registry.Selectable(p.MotionID) {
		fatal("motion_id %q is already deprecated and cannot start a new deprecation proposal", p.MotionID)
	}
	replacement, replacementErr := resolveDefinition(p.ReplacementID)
	if replacementErr != nil {
		fatal("replacement_id: %v", replacementErr)
	}
	targetsCompatible := true
	for _, target := range p.RequiredTargets {
		if !contains(definition.Targets, target) || !contains(replacement.Targets, target) {
			targetsCompatible = false
		}
	}

	declared := uniqueSorted(p.Consumers)
	actual := uniqueSorted(consumers)
	undeclared := difference(actual, declared)
	missing := difference(declared, actual)
	gate := "PASS_AUDIT_ONLY"
	if len(undeclared) > 0 || len(missing) > 0 || !targetsCompatible || !auditDate.Before(removeAfter) {
		gate = "BLOCKED"
	}
	result := report{
		Schema: reportSchema, Version: 1, MotionID: p.MotionID, ReplacementID: p.ReplacementID,
		Owner: p.Owner, DeprecatedOn: p.DeprecatedOn, RemoveAfter: p.RemoveAfter,
		AuditDate: auditDate.Format("2006-01-02"), RegistryPresent: true, Selectable: motion.Registry.Selectable(p.MotionID), ReplacementPresent: true,
		TargetsCompatible: targetsCompatible, Consumers: actual, DeclaredConsumers: declared,
		UndeclaredConsumers: undeclared, MissingConsumers: missing, WindowDays: window,
		Gate: gate,
		Limitations: []string{
			"This read-only audit does not write deprecated state, edit catalog data, remove registry entries, migrate plans, or authorize removal.",
			"Static text search cannot find dynamically assembled IDs, external consumers, untracked ignored content, or consumers outside this checkout.",
			"A PASS_AUDIT_ONLY result is not product/owner approval; removal still requires the declared compatibility window to expire and a separately reviewed migration.",
			"At compile level this package keeps old plans resolvable during the window; the runtime registry also supports a separately reviewed MarkDeprecated transition that removes the ID from selectable projections without breaking Resolve.",
		},
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatal("encode report: %v", err)
	}
	out = append(out, '\n')
	if *outPath == "" {
		_, err = os.Stdout.Write(out)
	} else {
		path := resolve(root, *outPath)
		err = os.MkdirAll(filepath.Dir(path), 0o755)
		if err == nil {
			err = os.WriteFile(path, out, 0o644)
		}
	}
	if err != nil {
		fatal("write report: %v", err)
	}
	if err := schemaval.ValidateFile(out, filepath.Join(root, reportSchemaPath)); err != nil {
		fatal("generated report schema: %v", err)
	}
	if gate == "BLOCKED" {
		os.Exit(2)
	}
}

func resolve(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func resolveDefinition(id string) (motion.MotionDefinition, error) {
	plugin, err := motion.Registry.Resolve(id)
	if err != nil {
		return motion.MotionDefinition{}, err
	}
	declarative, ok := plugin.(motion.DeclarativePlugin)
	if !ok {
		return motion.MotionDefinition{}, fmt.Errorf("motion %q is not declarative", id)
	}
	return declarative.Definition, nil
}

func discoverTextFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "out" || entry.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go", ".json", ".yaml", ".yml", ".toml", ".md", ".sh", ".py", ".txt", ".csv", ".tsx", ".ts", ".js", ".jsx":
			files = append(files, path)
		}
		return nil
	})
	return files
}

func findConsumers(root string, files []string, id string, excludedPaths ...string) []string {
	excluded := make(map[string]struct{}, len(excludedPaths))
	for _, path := range excludedPaths {
		excluded[filepath.Clean(path)] = struct{}{}
	}
	needle := []byte(id)
	var found []string
	for _, path := range files {
		if _, skip := excluded[filepath.Clean(path)]; skip || strings.HasSuffix(path, proposalSchema) {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil && isAuditReport(data) {
			continue // Generated evidence repeats IDs but is not a product consumer.
		}
		if isSelfAuditConsumer(path) {
			continue // Self-tests and deprecation behavior tests intentionally embed fixture IDs.
		}
		if err == nil && bytes.Contains(data, needle) {
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				found = append(found, filepath.ToSlash(rel))
			}
		}
	}
	return uniqueSorted(found)
}

func isSelfAuditConsumer(path string) bool {
	path = filepath.ToSlash(path)
	return strings.HasSuffix(path, "/cmd/motion-deprecation-audit/main_test.go") ||
		strings.HasSuffix(path, "/internal/motion/deprecation_test.go") ||
		strings.HasSuffix(path, "/internal/overlay/deprecation_catalog_test.go")
}

func isAuditReport(data []byte) bool {
	var header struct {
		Schema string `json:"schema"`
	}
	return json.Unmarshal(data, &header) == nil && header.Schema == reportSchema
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func difference(left, right []string) []string {
	allowed := make(map[string]struct{}, len(right))
	for _, value := range right {
		allowed[value] = struct{}{}
	}
	out := make([]string, 0)
	for _, value := range left {
		if _, ok := allowed[value]; !ok {
			out = append(out, value)
		}
	}
	return out
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "motion-deprecation-audit: "+format+"\n", args...)
	os.Exit(1)
}
