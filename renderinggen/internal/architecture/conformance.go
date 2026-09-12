// Package architecture owns the executable cross-repo conformance gate for the
// four non-negotiable boundaries:
//
//	PipelineGen  owns semantic intent      (renderinggen.overlay-plan.v1)
//	RenderingGen owns visual lowering      (chronon.render-plan.v2)
//	Chronon      owns physical execution   (one versioned plan format)
//	Storage/Queue own one wire contract each
//
// The gate scans the RenderingGen repository and, when they are checked out as
// siblings (the VeloxEditing workspace layout), the PipelineGen (refactored/)
// and Chronon3d/ trees too. It fails on NEW occurrences of a forbidden marker
// and on STALE baseline entries, so a fixed violation must ratchet the ledger
// down. Pre-existing occurrences live in testdata/conformance-baseline.txt.
//
// Baseline paths are relative to the RenderingGen repository root; sibling
// occurrences carry a `refactored/` or `Chronon3d/` prefix. A baseline entry
// whose file is absent (sibling repo not checked out) is ignored rather than
// reported stale, so the gate is correct in a standalone RenderingGen CI
// checkout and stricter in the full workspace.
package architecture

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Violation is one forbidden marker occurrence.
type Violation struct {
	Rule    string
	File    string // repo-relative, forward-slashed, with a sibling prefix when applicable
	Line    int    // 1-based; 0 when the rule is a filename rule
	Snippet string
}

// rule is one forbidden-marker definition.
type rule struct {
	id    string
	note  string
	exts  []string // applicable extensions; nil = every scanned file
	nodes []string // if set, the relative path must contain one of these
	// rootOnly scopes a rule to the RenderingGen repository. It is for
	// repo-hygiene rules ("this repository contains no machine-specific
	// path / no manual-splitting file") that are NOT part of the cross-repo
	// contract: a sibling repository's own hygiene is that repository's gate,
	// and flagging it here would force baselining another project's
	// pre-existing state into this ledger.
	rootOnly bool
	// filenameRe matches the file basename (line-less violations).
	filenameRe *regexp.Regexp
	// textRe matches a single source line.
	textRe *regexp.Regexp
}

// selfPkgRel is the repo-relative path of this package. Its source contains the
// marker literals as data, so it is skipped to keep the gate
// zero-violations-by-construction for its own declaration site.
const selfPkgRel = "renderinggen/internal/architecture"

// siblingRepos are the other repositories the gate knows about when they are
// checked out beside RenderingGen.
var siblingRepos = []string{"refactored", "Chronon3d"}

// scannedExts are the file kinds the gate reads.
var scannedExts = map[string]bool{
	".go": true, ".cpp": true, ".cc": true, ".c": true, ".hpp": true, ".h": true,
	".inc": true, ".md": true, ".json": true, ".yaml": true, ".yml": true,
	".sh": true, ".py": true, ".cmake": true, ".txt": true, ".service": true,
	".conf": true, ".schema": true, ".jsonc": true, ".proto": true,
}

// scannedNames are extension-less files the gate reads.
var scannedNames = map[string]bool{
	"Makefile": true, "go.mod": true, "go.work": true, "Dockerfile": true,
}

// skipDirs are directory basenames never descended into.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "build": true,
	".tmp": true, "tmp": true, "out": true, "artifacts": true, ".cache": true,
	".venv-whisper": true, ".venv-argos": true, "secrets": true,
	"results": true, ".codex": true,
}

// skipDir reports whether a directory basename is never descended into. Besides
// the exact names above it skips every build-* variant (build-local,
// build-debug, …) a native build emits: those trees are generated output, not
// source, and their CMake logs quote developer home paths.
func skipDir(name string) bool {
	if skipDirs[name] {
		return true
	}
	return strings.HasPrefix(name, "build-")
}

// maxFileBytes caps the size of a file the gate will read (generated timing
// sidecars and media metadata far exceed source size).
const maxFileBytes = 512 * 1024

// docExemptions are repository-root documentation files that legitimately NAME
// the forbidden markers (the published rules catalogue). They are skipped so
// the catalogue can describe what the gate bans. Operational code is never
// exempt: the gate's own package is skipped by selfPkgRel instead.
var docExemptions = map[string]bool{
	"CONFORMANCE.md": true,
}

// Rules returns the canonical rule set.
func Rules() []rule {
	// Pattern literals are assembled from fragments so this very file does not
	// contain every marker it bans (defence in depth on top of the self skip).
	return []rule{
		{
			id:     "render_plan_v1_schema",
			note:   "chronon.render-plan.v1 is deleted; the only physical output is chronon.render-plan.v2",
			textRe: mustRe("chronon\\.render-plan" + "\\.v1"),
		},
		{
			id:     "render_plan_unversioned_schema",
			note:   "unversioned \"chronon.render-plan\" is deleted; always name the versioned schema",
			textRe: mustRe(`"chronon\.render-plan"`),
		},
		{
			id:     "module_path_typo",
			note:   "the module path is RenderingGen (capital R, capital G); RenderginGen is a typo",
			textRe: mustRe("Rendergin" + "Gen"),
		},
		{
			id:       "hardcoded_home_path",
			rootOnly: true,
			note:     "code, configuration and docs resolve binaries/assets/install prefixes from env or a documented prefix, never a hardcoded developer home path (every source and config carrier, not only Go)",
			// The rule matches the SHAPE of a POSIX absolute home directory —
			// /home/<user>/ (Linux) or /Users/<user>/ (macOS) — not one
			// developer's login name. Encoding a machine fact (the login name)
			// in the gate made the rule a second source of truth that went stale
			// on any other machine and could be defeated by renaming the user.
			// Scope: source, configuration and documentation carriers — the files
			// a contributor copies from. Generated render artifacts (*_plan.json,
			// *.timing.json under the *_videos/ and testdata/debug/ trees) are DATA
			// produced by a render, not configuration, and their provenance (an
			// absolute machine path recorded by the renderer) is governed by the
			// tracking policy in .gitignore rather than by this rule.
			exts:   []string{".go", ".yaml", ".yml", ".sh", ".service", ".conf", ".md", ".py", ".cmake", ".proto", ".dockerfile"},
			textRe: mustRe(`(?:/home/|/Users/)[A-Za-z0-9._-]+/`),
		},
		{
			id:     "template_alias_org_default",
			note:   "ORG_DEFAULT is a dead alias; the canonical template id is ORGANIZATION_DEFAULT",
			textRe: mustRe("ORG_" + "DEFAULT"),
		},
		{
			id:     "template_alias_gpe_default",
			note:   "GPE_DEFAULT is a dead alias; the canonical template id is LOCATION_DEFAULT",
			textRe: mustRe("GPE_" + "DEFAULT"),
		},
		{
			id:     "entity_template_inference",
			note:   "kind is the authoritative discriminator; template_id must not classify entity/phrase/word",
			textRe: mustRe("isEntity" + "Template"),
		},
		{
			id:     "semantic_stats_second_pass",
			note:   "stats are produced by the single compile pass; downstream must reuse CompileResult.Stats",
			textRe: mustRe("SemanticStats" + `\(`),
		},
		{
			id:         "partnn_filename",
			rootOnly:   true,
			note:       "files are named for responsibility; *_partNN.* is a manual-splitting artifact (applies to every scanned file, not only Go)",
			filenameRe: mustRe(`_part[0-9][0-9]`),
		},
		{
			id:     "legacy_layer_preset_field",
			note:   "the render-plan layer carries preset_id/its visual lowering, never a bare json:\"preset\" slot",
			nodes:  []string{"chronon"},
			textRe: mustRe(`json:"preset"`),
		},
	}
}

func mustRe(p string) *regexp.Regexp { return regexp.MustCompile(p) }

// escapedQuotes is the escaping that hides a marker from a textual rule.
// A shell script or YAML that embeds a JSON document escapes its quotes
// (\"chronon.render-plan\"), and a regex looking for "chronon.render-plan"
// cannot see it through the backslash. A live e2e script reintroduced the
// forbidden unversioned schema and passed the gate exactly this way, so the
// rules match the UNESCAPED projection of every line.
var escapedQuotes = strings.NewReplacer(`\"`, `"`, `\'`, `'`)

// normalizeForMatch returns the projection of a source line the rules match
// against. The unmatched, raw line is still what gets reported as a snippet,
// so a finding always points at the real file content.
func normalizeForMatch(line string) string {
	if !strings.Contains(line, `\`) {
		return line
	}
	return escapedQuotes.Replace(line)
}

// scannedFile reports whether path is a file the gate reads.
func scannedFile(name string) bool {
	if scannedNames[name] {
		return true
	}
	return scannedExts[strings.ToLower(filepath.Ext(name))]
}

// Target is one scanned tree with the path prefix its files carry.
type Target struct {
	Dir    string
	Prefix string // "" for RenderingGen, "refactored/" or "Chronon3d/" for siblings
}

// PackageDir returns the on-disk directory of this package.
func PackageDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(file)
}

// RepoRoot returns the RenderingGen repository root that contains this module.
func RepoRoot() string {
	// <repo>/renderinggen/internal/architecture -> <repo>
	return filepath.Clean(filepath.Join(PackageDir(), "..", "..", ".."))
}

// TargetOverride returns a single-target override from CONFORMANCE_ROOT, or
// nil when unset.
func TargetOverride() []Target {
	env := strings.TrimSpace(os.Getenv("CONFORMANCE_ROOT"))
	if env == "" {
		return nil
	}
	abs, err := filepath.Abs(env)
	if err != nil {
		abs = env
	}
	return []Target{{Dir: abs, Prefix: ""}}
}

// Targets returns the scanned trees: the RenderingGen repo, plus any sibling
// repos that are checked out beside it.
func Targets() []Target {
	if override := TargetOverride(); override != nil {
		return override
	}
	repo := RepoRoot()
	targets := []Target{{Dir: repo, Prefix: ""}}
	parent := filepath.Dir(repo)
	for _, name := range siblingRepos {
		dir := filepath.Join(parent, name)
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			targets = append(targets, Target{Dir: dir, Prefix: name + "/"})
		}
	}
	return targets
}

// AbsentSibling reports whether a baseline entry belongs to a sibling
// repository (refactored/, Chronon3d/) that is NOT checked out here. Such an
// entry is unverifiable in this checkout and must be ignored rather than
// reported stale — the standalone-CI correctness rule.
//
// The converse is equally load-bearing: a REPO-LOCAL entry whose file no
// longer exists is stale, because someone deleted the violation without
// ratcheting the ledger down. Treating "file not found" as ignorable
// everywhere (the historical behavior) meant a deleted file made its ledger
// line immortal and the ratchet could never contract.
func AbsentSibling(rel string) bool {
	relSlash := filepath.ToSlash(rel)
	for _, name := range siblingRepos {
		if !strings.HasPrefix(relSlash, name+"/") {
			continue
		}
		dir := filepath.Join(filepath.Dir(RepoRoot()), name)
		st, err := os.Stat(dir)
		return err != nil || !st.IsDir()
	}
	return false
}

// PathExists resolves a baseline path (with its target prefix) to disk.
func PathExists(rel string) bool {
	relSlash := filepath.ToSlash(rel)
	for _, t := range Targets() {
		if t.Prefix == "" {
			continue
		}
		if strings.HasPrefix(relSlash, t.Prefix) {
			trimmed := strings.TrimPrefix(relSlash, t.Prefix)
			_, err := os.Stat(filepath.Join(t.Dir, filepath.FromSlash(trimmed)))
			return err == nil
		}
	}
	_, err := os.Stat(filepath.Join(RepoRoot(), filepath.FromSlash(relSlash)))
	return err == nil
}

// ScanAll scans every Target and returns the deterministic violation set.
func ScanAll() ([]Violation, error) {
	var out []Violation
	for _, t := range Targets() {
		vs, err := scanTarget(t)
		if err != nil {
			return nil, err
		}
		out = append(out, vs...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Rule < out[j].Rule
	})
	return out, nil
}

// ScanTargets scans an explicit target list (used by unit tests).
func ScanTargets(targets []Target) ([]Violation, error) {
	var out []Violation
	for _, t := range targets {
		vs, err := scanTarget(t)
		if err != nil {
			return nil, err
		}
		out = append(out, vs...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Rule < out[j].Rule
	})
	return out, nil
}

func scanTarget(t Target) ([]Violation, error) {
	rules := Rules()
	var out []Violation

	err := filepath.WalkDir(t.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(t.Dir, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			if relSlash == "." {
				return nil
			}
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if t.Prefix == "" && relSlash == selfPkgRel {
				return filepath.SkipDir
			}
			return nil
		}
		fileRel := t.Prefix + relSlash
		if t.Prefix == "" && strings.HasPrefix(relSlash, selfPkgRel+"/") {
			return nil
		}
		if t.Prefix == "" && docExemptions[relSlash] {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > maxFileBytes {
			return nil
		}
		if !scannedFile(d.Name()) {
			return nil
		}
		for _, ru := range rules {
			if ru.filenameRe == nil || !ru.applies(fileRel) {
				continue
			}
			if ru.filenameRe.MatchString(d.Name()) {
				out = append(out, Violation{Rule: ru.id, File: fileRel})
			}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		textRules := make([]rule, 0, len(rules))
		for _, ru := range rules {
			if ru.textRe != nil && ru.applies(fileRel) {
				textRules = append(textRules, ru)
			}
		}
		if len(textRules) == 0 {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			matchLine := normalizeForMatch(line)
			for _, ru := range textRules {
				if ru.textRe.MatchString(matchLine) {
					out = append(out, Violation{
						Rule:    ru.id,
						File:    fileRel,
						Line:    i + 1,
						Snippet: strings.TrimSpace(line),
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r rule) applies(relSlash string) bool {
	if r.rootOnly && (strings.HasPrefix(relSlash, "refactored/") || strings.HasPrefix(relSlash, "Chronon3d/")) {
		return false
	}
	if len(r.nodes) > 0 {
		ok := false
		for _, n := range r.nodes {
			if strings.Contains(relSlash, n) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(r.exts) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(relSlash))
	for _, e := range r.exts {
		if ext == e {
			return true
		}
	}
	return false
}

// NoteFor returns the human note for a rule id.
func NoteFor(id string) string {
	for _, r := range Rules() {
		if r.id == id {
			return r.note
		}
	}
	return ""
}

// Format renders a violation for test output.
func (v Violation) Format() string {
	loc := v.File
	if v.Line > 0 {
		loc = fmt.Sprintf("%s:%d", v.File, v.Line)
	}
	return fmt.Sprintf("%s: %s (%s)", loc, NoteFor(v.Rule), v.Rule)
}
