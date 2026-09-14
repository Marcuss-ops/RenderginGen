// op_bound_test.go pins the invariant the repository contracts document but
// cannot express in Go: the persistence interfaces carry NO context, so every
// PostgreSQL operation must bound ITSELF.
//
// The contracts' own words are "a backend must bound the work it performs (the
// PostgreSQL backend derives a per-operation deadline — see its opContext)".
// Without a test that is a comment. One new method that calls
// db.ExecContext(context.Background(), …) puts an unbounded query back on the
// availability-critical path, where the caller (an HTTP handler, a worker
// goroutine) then waits on a lock wait or a mid-failover server indefinitely —
// and the contract looks unchanged, so nothing else would notice.
//
// The check is structural, not textual: it parses every repository contract and
// this package, resolves each exported *Repository method to the deadline it
// derives (directly, or through a delegation chain like Claim -> ClaimState),
// and requires every contract method to be bound that way.
//
// On the deferred context migration: threading `ctx` through
// JobRepository -> service -> server was measured at 20 interface methods x 3
// implementations, ~125 production and ~281 test call sites. It is deliberately
// NOT done as a drive-by — a half-finished pass leaves an availability-critical
// module unbuildable, and it must not share a branch with other work. What makes
// deferring it safe is this file: the bound it would provide is already enforced
// here, structurally, over every contract and every *Repository method, with
// delegation resolved and ghost exceptions rejected. Do the migration as its own
// change, one package per step; until then a new unbound method cannot land.
package postgres

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// boundedOperationExceptions are the exported *Repository methods that
// legitimately do no I/O and therefore need no deadline. Every entry must name
// a method that really exists (checked below), so a renamed or deleted method
// cannot leave a ghost permission behind.
var boundedOperationExceptions = map[string]string{
	"SetOpTimeout": "configures the bound itself; it performs no I/O",
}

// TestEveryRepositoryOperationIsBounded requires a deadline in every method the
// repository contracts declare.
func TestEveryRepositoryOperationIsBounded(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	contract, err := contractMethods(filepath.Join(dir, ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(contract) == 0 {
		t.Fatal("no methods found in the repository contracts; the check below would be vacuous")
	}

	methods, err := repositoryMethods(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) == 0 {
		t.Fatal("no exported *Repository methods found; the check below would be vacuous")
	}

	seed := 0
	for _, method := range methods {
		if method.callsOpContext {
			seed++
		}
	}
	if seed == 0 {
		t.Fatal("no method derives a deadline from opContext(); every bound below would be vacuous")
	}
	bounded := closeOverDelegation(methods)

	// (1) Every method a contract declares must be implemented and bounded.
	missing := make([]string, 0, len(contract))
	for method := range contract {
		if _, isBounded := bounded[method]; isBounded {
			continue
		}
		if _, exempt := boundedOperationExceptions[method]; exempt {
			continue
		}
		missing = append(missing, method)
	}
	sort.Strings(missing)
	for _, method := range missing {
		if !methods[method].exists {
			t.Errorf("contract declares %s but no exported *Repository method implements it", method)
			continue
		}
		t.Errorf("%s implements %s but derives no deadline from opContext(): the contract carries no ctx, so the operation must bound itself", methods[method].where, method)
	}

	// (2) A ghost exception is a permission nobody needs, and it hides the
	// method it was written for.
	for method, reason := range boundedOperationExceptions {
		if _, ok := methods[method]; !ok {
			t.Errorf("exception %q (%s) matches no exported *Repository method; delete it", method, reason)
		}
	}

	// (3) A bounded method that no contract declares is either a missing
	// contract entry or a helper that should not be exported.
	for method, entry := range bounded {
		if _, declared := contract[method]; !declared {
			t.Errorf("%s is bounded but is not declared by any repository contract; add it to a contract or unexport it", entry.where)
		}
	}

	for method, entry := range methods {
		if entry.callsOpContext {
			t.Logf("deadline source: %s (%s)", method, entry.where)
		}
	}
}

// TestOperationBoundIsADeadline pins what opContext() actually hands the driver,
// and keeps SetOpTimeout honest: the contract test above grants it an exception
// as a method, so it must be exercised rather than merely declared (an unused
// knob is how this file's own exception entry nearly became a ghost).
//
// The promise is a BOUND. A timeout that was configured but never turned into a
// context deadline would leave operations unbounded while looking configured —
// worse than having no knob at all, because it would pass review.
func TestOperationBoundIsADeadline(t *testing.T) {
	if defaultOpTimeout <= 0 {
		t.Fatalf("defaultOpTimeout = %s: the fallback itself must bound the operation", defaultOpTimeout)
	}

	repo := &Repository{opTimeout: 50 * time.Millisecond}
	ctx, cancel := repo.opContext()
	defer cancel()
	assertOpDeadlineWithin(t, ctx, 50*time.Millisecond, "configured bound", 0)

	// A non-positive override must never mean "unbounded": opContext falls back
	// to the default instead. The lower bound proves the override was actually
	// cleared rather than the previous (tiny) bound still being in force.
	repo.SetOpTimeout(0)
	ctx, cancel = repo.opContext()
	defer cancel()
	assertOpDeadlineWithin(t, ctx, defaultOpTimeout, "zero override", 50*time.Millisecond)

	repo.SetOpTimeout(2 * time.Second)
	ctx, cancel = repo.opContext()
	defer cancel()
	assertOpDeadlineWithin(t, ctx, 2*time.Second, "explicit override", 50*time.Millisecond)
}

// assertOpDeadlineWithin requires ctx to carry a deadline in (min, max].
func assertOpDeadlineWithin(t *testing.T, ctx context.Context, max time.Duration, what string, min time.Duration) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("%s: opContext() returned no deadline, so the operation would be unbounded", what)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > max {
		t.Fatalf("%s: deadline %s away, want (0, %s]", what, remaining, max)
	}
	if min > 0 && remaining <= min {
		t.Fatalf("%s: deadline %s away, want more than %s", what, remaining, min)
	}
}

// repositoryMethod is one exported *Repository method in this package.
type repositoryMethod struct {
	where           string
	callsOpContext  bool
	callsRepository map[string]bool // other *Repository methods it delegates to
	exists          bool
}

// repositoryMethods parses every non-test file of this package and describes
// each exported *Repository method.
func repositoryMethods(dir string) (map[string]*repositoryMethod, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	methods := map[string]*repositoryMethod{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() || !repositoryReceiver(fn.Recv) {
				continue
			}
			entry := &repositoryMethod{
				where:           fmt.Sprintf("%s:%d", name, fset.Position(fn.Pos()).Line),
				callsRepository: map[string]bool{},
				exists:          true,
			}
			ast.Inspect(fn, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				recv, ok := selector.X.(*ast.Ident)
				if !ok || recv.Name != "r" {
					return true
				}
				if selector.Sel.Name == "opContext" {
					entry.callsOpContext = true
					return true
				}
				entry.callsRepository[selector.Sel.Name] = true
				return true
			})
			methods[fn.Name.Name] = entry
		}
	}
	return methods, nil
}

// closeOverDelegation returns the methods that reach opContext() directly or by
// delegating to a method that does (Claim -> ClaimState). Delegation is a
// legitimate way to bound an operation: the point is that the work is bounded,
// not that the deadline is written twice.
func closeOverDelegation(methods map[string]*repositoryMethod) map[string]*repositoryMethod {
	bounded := map[string]*repositoryMethod{}
	for method, entry := range methods {
		if entry.callsOpContext {
			bounded[method] = entry
		}
	}
	for grew := true; grew; {
		grew = false
		for method, entry := range methods {
			if _, already := bounded[method]; already {
				continue
			}
			for callee := range entry.callsRepository {
				if _, ok := bounded[callee]; ok {
					bounded[method] = entry
					grew = true
					break
				}
			}
		}
	}
	return bounded
}

// contractMethods returns the method names declared by every interface of every
// repository contract file, mapped to the interface that declares them.
func contractMethods(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	methods := map[string]string{}
	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse contract %s: %w", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			iface, ok := spec.Type.(*ast.InterfaceType)
			if !ok || iface.Methods == nil {
				return true
			}
			found++
			for _, field := range iface.Methods.List {
				for _, ifaceMethod := range field.Names {
					if ifaceMethod.IsExported() {
						methods[ifaceMethod.Name] = spec.Name.Name
					}
				}
			}
			return true
		})
	}
	if found == 0 {
		return nil, fmt.Errorf("no interfaces found under %s; the contract scan lost its target", dir)
	}
	return methods, nil
}

// repositoryReceiver reports whether a method belongs to *Repository.
func repositoryReceiver(recv *ast.FieldList) bool {
	if len(recv.List) == 0 {
		return false
	}
	switch typ := recv.List[0].Type.(type) {
	case *ast.StarExpr:
		ident, ok := typ.X.(*ast.Ident)
		return ok && ident.Name == "Repository"
	case *ast.Ident:
		return typ.Name == "Repository"
	}
	return false
}
