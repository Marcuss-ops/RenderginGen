package architecture

// dead-export ratchet.
//
// The 2026-09-14 audit that removed PlanChunks and wired SetRequeueRetry was a
// one-off: it scanned queue + renderinggen by hand, found real things, and left
// nothing behind that would find the NEXT one. A zero-caller export is
// invisible to the compiler (it is exported, so `unused`-style linters stay
// quiet), invisible to coverage (nothing runs it), and invisible in review (the
// declaration reads as public API). The only durable way to keep it out is a
// check that runs on every push.
//
// This is that check, extended to every RenderingGen module. It is a SOURCE
// scan over the RenderingGen checkout alone, for the same reason the
// conformance gate is: CI checks out this repository and no sibling, so a check
// that needed refactored/ or Chronon3d/ would be a check that only sometimes
// runs.
//
// What counts as a reference. The declaration itself never counts, and neither
// does a mention in a comment or a string — identifiers are counted from real
// tokens, not from text, because a symbol documented as "used by" is exactly
// the shape a dead one hides behind. Beyond that:
//
//   - a package-level func/type/var/const is live when it is referenced
//     unqualified inside its own package directory, or as `pkg.Name` from
//     anywhere else (the two ordinary call forms);
//   - a METHOD is live when its name is referenced anywhere, because methods
//     are invoked through a value or an interface, never as `pkg.Name`. That
//     also keeps interface implementations live: `Service` calls
//     `repo.SubmitIdempotent`, so the memory repository's implementation is
//     reachable even though its own package never names it.
//   - a METHOD named in stdlibInterfaceMethods (today only encoding/json's
//     UnmarshalJSON) is live when the interface it implements is asserted in
//     the corpus. The library invokes that method through reflection, so no Go
//     file can ever contain the call site the identifier count looks for; the
//     `var _ json.Unmarshaler = (*T)(nil)` assertion is the one place the
//     wiring can be stated, and it is a deliberate claim about the code rather
//     than a name that happens to reappear. Without this rule the check reports
//     a false positive the moment a genuinely dead sibling method with the same
//     name is deleted, which is what happened to semanticItem.UnmarshalJSON.
//
// The check is deliberately conservative in the direction of false negatives:
// a name reused by a live sibling can mask a dead one. It is exact for the case
// it exists to catch — a symbol that appears nowhere but its own declaration.

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// deadExportModules are the RenderingGen modules the ratchet covers, relative
// to the repository root. It is the whole repository on purpose: the audit that
// motivated this check spanned two of the three modules, and the objectstore
// binary is small enough that "no dead exports" is a cheap promise to keep.
var deadExportModules = []string{"renderinggen", "queue", "objectstore"}

// deadExportExceptions names exported declarations that are legitimately
// unreferenced from inside RenderingGen. It is empty today and should stay
// that way: every entry is a standing permission, so adding one is a claim that
// the CONSUMER lives outside this repository, and the reason must name it. A
// ghost entry (naming a symbol that no longer exists) is an error, so a
// carve-out cannot silently outlive the code it was written for.
var deadExportExceptions = map[string]string{}

// stdlibInterfaceMethods maps a standard-library interface to the method it
// invokes without a call site in the consumer's source. Only the entries that
// are needed belong here: this is an extension point for the next reflection
// hook, not a catalogue, because an unused entry can only widen the
// false-negative window the scan already tolerates.
var stdlibInterfaceMethods = map[string][]string{
	"Unmarshaler": {"UnmarshalJSON"},
}

// interfaceHookRefs counts the corpus-wide assertions of every standard-library
// interface that requires a method with this name. An implementation without
// its assertion contributes zero, so it stays reported as dead.
func interfaceHookRefs(method string, allIdents map[string]int) int {
	hookRefs := 0
	for iface, methods := range stdlibInterfaceMethods {
		for _, required := range methods {
			if required == method {
				hookRefs += allIdents[iface]
			}
		}
	}
	return hookRefs
}

// skippedCorpusDirs are directory names the scan never reads. testdata is
// skipped because it holds fixtures (including Go sources parsed as data), and
// treating a fixture as a declaration would produce noise, not signal.
var skippedCorpusDirs = map[string]bool{
	".git":     true,
	".tmp":     true,
	"testdata": true,
	"vendor":   true,
}

// deadExport identifies one exported top-level declaration.
type deadExport struct {
	Name   string // exported identifier
	Pkg    string // package name that declares it
	Dir    string // repo-relative, forward-slashed directory
	File   string // repo-relative, forward-slashed file
	Method bool   // declared on a receiver, so it is called through a value
}

func (d deadExport) String() string {
	kind := "func/type/var/const"
	if d.Method {
		kind = "method"
	}
	return d.File + ": " + d.Name + " (" + kind + ")"
}

// deadExportScan is the scan result.
type deadExportScan struct {
	declared []deadExport // every covered declaration, sorted
	dead     []deadExport // declarations referenced nowhere, sorted
}

var packageClauseRe = regexp.MustCompile(`(?m)^package\s+([A-Za-z_][A-Za-z0-9_]*)`)

// scanDeadExports walks root and reports every exported top-level declaration
// in one of modules that no Go file under root references.
func scanDeadExports(root string, modules []string) (*deadExportScan, error) {
	files, err := goFilesUnder(root)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go files under %s: the scan found no corpus", root)
	}

	inModule := func(rel string) bool {
		for _, m := range modules {
			if rel == m || strings.HasPrefix(rel, m+"/") {
				return true
			}
		}
		return false
	}

	// Corpus: identifier and qualified-identifier counts per file, taken from
	// real tokens so comments and strings contribute nothing.
	type corpusFile struct {
		dir   string
		ident map[string]int
		qual  map[string]int
	}
	corpus := make([]corpusFile, 0, len(files))
	// allIdents is the whole-corpus count, which is what a method is checked
	// against regardless of which directory references it.
	allIdents := map[string]int{}
	declared := map[string]deadExport{}

	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		dir := filepath.ToSlash(filepath.Dir(rel))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		ident, qual := countIdentTokens(path, raw)
		for name, n := range ident {
			allIdents[name] += n
		}
		corpus = append(corpus, corpusFile{dir: dir, ident: ident, qual: qual})

		if strings.HasSuffix(rel, "_test.go") || !inModule(rel) {
			continue
		}
		for _, d := range exportedDecls(rel, dir, raw) {
			declared[d.Dir+"."+d.Name] = d
		}
	}

	if len(declared) == 0 {
		return nil, fmt.Errorf("no exported declarations found in %v under %s; the ratchet would pass vacuously", modules, root)
	}

	scan := &deadExportScan{}
	for _, d := range declared {
		scan.declared = append(scan.declared, d)
	}
	sort.Slice(scan.declared, func(i, j int) bool {
		if scan.declared[i].Dir != scan.declared[j].Dir {
			return scan.declared[i].Dir < scan.declared[j].Dir
		}
		return scan.declared[i].Name < scan.declared[j].Name
	})

	for _, d := range scan.declared {
		refs := 0
		if d.Method {
			refs = allIdents[d.Name] + interfaceHookRefs(d.Name, allIdents)
		} else {
			for _, cf := range corpus {
				if cf.dir == d.Dir {
					refs += cf.ident[d.Name]
					continue
				}
				refs += cf.qual[d.Pkg+"."+d.Name]
			}
		}
		// The declaration itself is one identifier occurrence.
		if refs <= 1 {
			scan.dead = append(scan.dead, d)
		}
	}
	sort.Slice(scan.dead, func(i, j int) bool { return scan.dead[i].String() < scan.dead[j].String() })
	return scan, nil
}

// goFilesUnder returns every .go file under root, skipping build/scratch dirs.
func goFilesUnder(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedCorpusDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// countIdentTokens returns the identifier occurrence counts and the
// `pkg.Name` qualified-occurrence counts of one Go file. It uses go/scanner,
// so a name that appears only inside a comment or a string literal — the
// classic shape of a stale "this is used by X" note — is not a reference.
func countIdentTokens(path string, raw []byte) (map[string]int, map[string]int) {
	ident := map[string]int{}
	qual := map[string]int{}

	fset := token.NewFileSet()
	file := fset.AddFile(path, fset.Base(), len(raw))
	var s scanner.Scanner
	s.Init(file, raw, nil, scanner.ScanComments)
	// s.Init is documented to be error-free; a nil scanner.ErrorList is fine.

	lastIdent := ""
	afterDot := false
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		switch tok {
		case token.IDENT:
			ident[lit]++
			if afterDot && lastIdent != "" {
				qual[lastIdent+"."+lit]++
			}
			lastIdent = lit
			afterDot = false
		case token.PERIOD:
			afterDot = lastIdent != ""
		case token.COMMENT:
			// Comments are not references; keep the pending state untouched so
			// `a /* c */ .b` still reads as a qualified name.
		default:
			lastIdent = ""
			afterDot = false
		}
	}
	return ident, qual
}

// exportedDecls returns the exported top-level declarations of one non-test Go
// file: funcs, methods, types, vars and consts, including entries of a
// `var (` / `const (` / `type (` block.
func exportedDecls(rel, dir string, raw []byte) []deadExport {
	pkg := ""
	if m := packageClauseRe.FindSubmatch(raw); m != nil {
		pkg = string(m[1])
	}
	if pkg == "" {
		return nil
	}

	var out []deadExport
	seen := map[string]bool{}
	add := func(name string, method bool) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, deadExport{Name: name, Pkg: pkg, Dir: dir, File: rel, Method: method})
	}

	// A declaration block starts at column 0 and its entries are indented,
	// which is what separates them from the same keywords inside a function.
	inBlock := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if inBlock {
			if trimmed == ")" || (line == trimmed && trimmed != "") {
				inBlock = false
				continue
			}
			add(leadingExported(trimmed), false)
			continue
		}
		if line == trimmed {
			switch trimmed {
			case "var (", "const (", "type (":
				inBlock = true
				continue
			}
		}
		name, method := exportedDeclName(line)
		add(name, method)
	}
	return out
}

// exportedDeclName extracts the exported identifier from a top-level
// declaration line, plus whether it is a method. It returns "" for anything
// that declares nothing exported.
func exportedDeclName(line string) (string, bool) {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "func "):
		rest := strings.TrimPrefix(line, "func ")
		if strings.HasPrefix(rest, "(") {
			close := strings.Index(rest, ")")
			if close < 0 {
				return "", false
			}
			return leadingExported(strings.TrimSpace(rest[close+1:])), true
		}
		return leadingExported(rest), false
	case strings.HasPrefix(line, "type "), strings.HasPrefix(line, "var "), strings.HasPrefix(line, "const "):
		rest := line[strings.IndexByte(line, ' ')+1:]
		return leadingExported(rest), false
	}
	return "", false
}

// leadingExported returns the leading exported identifier of an indented
// declaration entry, or "" when the text does not begin with one.
func leadingExported(rest string) string {
	name := rest
	if i := strings.IndexAny(rest, " \t(["); i >= 0 {
		name = rest[:i]
	}
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return ""
	}
	for _, r := range name {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return ""
	}
	return name
}

// TestNoDeadExports is the ratchet. It fails when an exported declaration in a
// RenderingGen module is referenced nowhere in the checkout, and when an
// exception entry names a symbol that no longer exists.
func TestNoDeadExports(t *testing.T) {
	root := RepoRoot()
	scan, err := scanDeadExports(root, deadExportModules)
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}

	declared := map[string]bool{}
	for _, d := range scan.declared {
		declared[d.Name] = true
	}
	for name, reason := range deadExportExceptions {
		if !declared[name] {
			t.Errorf("exception %q (%s) matches no exported declaration in %v; delete it so the carve-out cannot outlive the code", name, reason, deadExportModules)
		}
	}

	for _, d := range scan.dead {
		if reason, ok := deadExportExceptions[d.Name]; ok {
			t.Logf("zero-caller export (excepted): %s — %s", d, reason)
			continue
		}
		t.Errorf("exported %s is referenced nowhere in RenderingGen: wire it, unexport it, or delete it. A zero-caller export is either dead code or a contract nobody honours. If its consumer lives outside this repository, add it to deadExportExceptions with the reason.", d)
	}
}

// TestDeadExportScanSeesStdlibInterfaceHooks pins the one rule the identifier
// count cannot reach. encoding/json invokes UnmarshalJSON through reflection, so
// a type that implements the hook has no call site that any file could name; the
// rule fires on the `var _ json.Unmarshaler = (*T)(nil)` assertion, which is a
// deliberate claim about the code. It must NOT fire without one, otherwise the
// carve-out would be indistinguishable from a method nobody calls.
func TestDeadExportScanSeesStdlibInterfaceHooks(t *testing.T) {
	// The two corpora differ by exactly the assertion line.
	corpus := func(assertion string) string {
		return `package mod

import "encoding/json"

type item struct{}

// UnmarshalJSON is the wire contract for item.
func (i *item) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, i) }
` + assertion
	}

	methodIsDead := func(body string) bool {
		t.Helper()
		root := t.TempDir()
		path := filepath.Join(root, "mod", "item.go")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		scan, err := scanDeadExports(root, []string{"mod"})
		if err != nil {
			t.Fatalf("scan synthetic corpus: %v", err)
		}
		for _, d := range scan.dead {
			if d.Name == "UnmarshalJSON" {
				return true
			}
		}
		return false
	}

	t.Run("the interface assertion keeps the reflection hook live", func(t *testing.T) {
		if body := corpus("\nvar _ json.Unmarshaler = (*item)(nil)\n"); methodIsDead(body) {
			t.Errorf("an asserted json.Unmarshaler implementation was reported dead; encoding/json calls it reflectively, so the assertion is its only visible wiring")
		}
	})

	t.Run("without the assertion the same method stays dead", func(t *testing.T) {
		if body := corpus(""); !methodIsDead(body) {
			t.Errorf("an UnmarshalJSON implementation that nothing asserts and nothing calls was reported live; the hook rule must key on the assertion, not on the method name")
		}
	})
}

// TestDeadExportScanDetectsSyntheticCase proves the scanner fires, and that it
// still distinguishes the cases it must: a comment is not a reference, an
// unqualified same-package call is, a `pkg.Name` call from elsewhere is, and a
// same-named LIVE declaration in another package must not carry a dead one.
// The corpus is synthetic on purpose — the repository currently has zero dead
// exports, so a canary would have to be introduced to test the check.
func TestDeadExportScanDetectsSyntheticCase(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("mod/live.go", `package mod

// Live is referenced by the sibling file in this package.
func Live() {}

// LiveQualified is referenced as mod.LiveQualified from another package.
func LiveQualified() {}
`)
	write("mod/use.go", `package mod

func useLive() { Live() }
`)
	write("mod/impl.go", `package mod

type impl struct{}

// Run implements runner and is only ever called through the interface value,
// so it is never named inside its own package.
func (impl) Run() {}
`)
	write("mod/dead.go", `package mod

// DeadNothing is declared, documented, and called by nobody. The comment
// naming DeadNothing again must not keep it alive.
func DeadNothing() {}

// DeadShadowed shares its name with a LIVE declaration in another package and
// must still be reported: a live other.DeadShadowed must not carry a dead
// mod.DeadShadowed.
func DeadShadowed() {}
`)
	write("consumer/consumer.go", `package consumer

func consume(r runner) {
	mod.LiveQualified()
	other.DeadShadowed()
	r.Run()
}
`)
	write("other/other.go", `package other

// DeadShadowed is live because consumer/ references other.DeadShadowed.
func DeadShadowed() {}
`)

	scan, err := scanDeadExports(root, []string{"mod", "consumer", "other"})
	if err != nil {
		t.Fatalf("scan synthetic corpus: %v", err)
	}

	dead := map[string]bool{}
	for _, d := range scan.dead {
		dead[d.Dir+"."+d.Name] = true
	}
	for _, mustBeDead := range []string{"mod.DeadNothing", "mod.DeadShadowed"} {
		if !dead[mustBeDead] {
			t.Errorf("scanner missed a zero-caller export %s; dead=%v", mustBeDead, dead)
		}
	}
	for _, mustBeLive := range []string{"mod.Live", "mod.LiveQualified", "mod.Run", "other.DeadShadowed"} {
		if dead[mustBeLive] {
			t.Errorf("scanner reported %s dead, but it is referenced; dead=%v", mustBeLive, dead)
		}
	}
}
