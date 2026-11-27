package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestProcessorTestsDoNotAssignPackageState enforces the RGA-12 rule: a test
// seam is a per-instance dependency, never an assignment to a package variable.
// The processor used to expose `var validateDecode = media.ValidateDecode` and
// friends so tests could swap them; that is a data race waiting for a
// concurrent test and a stub that leaks into unrelated processors. Swapping the
// fields is now possible through the struct, so the package-level form is
// banned mechanically.
//
// The scan is syntactic on purpose: it must also catch a future seam that is not
// a function today. A test may still READ package variables and call package
// functions; only assignment to one is rejected.
func TestProcessorTestsDoNotAssignPackageState(t *testing.T) {
	dir := filepath.Join("..", "processor")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("processor package not found: %v", err)
	}

	declared, err := packageVariables(filepath.Join(dir))
	if err != nil {
		t.Fatalf("scan package variables: %v", err)
	}
	if len(declared) == 0 {
		t.Fatal("no package variables found in the processor package; the scanner is broken")
	}

	var violations []string
	err = filepath.Walk(filepath.Join(dir), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				if _, hit := declared[ident.Name]; hit {
					position := token.NewFileSet().Position(assign.Pos())
					violations = append(violations, path+":"+position.String()+": test assigns package variable "+ident.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan processor tests: %v", err)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("test files must not mutate package state:\n%s", strings.Join(violations, "\n"))
	}
}

// packageVariables collects the names of every package-scope var declared in
// the non-test Go files under dir.
func packageVariables(dir string) (map[string]struct{}, error) {
	names := make(map[string]struct{})
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				spec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range spec.Names {
					names[name.Name] = struct{}{}
				}
			}
		}
		return nil
	})
	return names, err
}
