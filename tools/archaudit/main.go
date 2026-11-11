// Command archaudit measures one Go package for the architectural audit:
// file/LOC size, per-function cyclomatic complexity (branch-count heuristic)
// and cross-file references between top-level declarations. It is deliberately
// stdlib-only so it runs in a checkout without extra modules.
//
//	go run tools/archaudit/main.go -dir renderinggen/internal/overlay
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fileInfo struct {
	path     string
	baseterm string
	lines    int
	bytes    int64
	funcs    int
	test     bool
	ast      *ast.File
}

type funcInfo struct {
	name       string
	file       string
	line       int
	lines      int
	complexity int
}

type edge struct {
	from  string
	to    string
	count int
}

func main() {
	dir := flag.String("dir", "renderinggen/internal/overlay", "package directory to analyze")
	topFuncs := flag.Int("top-funcs", 20, "number of highest-complexity functions")
	topEdges := flag.Int("top-edges", 20, "number of strongest cross-file edges")
	focus := flag.String("focus", "compiler.go", "file to detail with its functions")
	flag.Parse()

	paths, err := filepath.Glob(filepath.Join(*dir, "*.go"))
	if err != nil || len(paths) == 0 {
		fmt.Fprintf(os.Stderr, "no .go files in %s\n", *dir)
		os.Exit(1)
	}
	sort.Strings(paths)

	fset := token.NewFileSet()
	files := make([]*fileInfo, 0, len(paths))
	declOwner := make(map[string]string)
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
			os.Exit(1)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
			os.Exit(1)
		}
		info := &fileInfo{
			path:  path,
			lines: strings.Count(string(src), "\n"),
			bytes: int64(len(src)),
			test:  strings.HasSuffix(path, "_test.go"),
			ast:   file,
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				info.funcs++
				name := d.Name.Name
				if d.Recv != nil && len(d.Recv.List) == 1 {
					name = receiverName(d.Recv.List[0].Type) + "." + name
				}
				declOwner[name] = path
				declOwner[d.Name.Name] = path
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						declOwner[s.Name.Name] = path
					case *ast.ValueSpec:
						for _, n := range s.Names {
							declOwner[n.Name] = path
						}
					}
				}
			}
		}
		files = append(files, info)
	}

	funcs := make([]funcInfo, 0)
	for _, info := range files {
		for _, decl := range info.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				name = receiverName(fn.Recv.List[0].Type) + "." + name
			}
			pos := fset.Position(fn.Pos())
			end := fset.Position(fn.End())
			funcs = append(funcs, funcInfo{
				name:       name,
				file:       info.path,
				line:       pos.Line,
				lines:      end.Line - pos.Line + 1,
				complexity: complexity(fn.Body),
			})
		}
	}
	sort.Slice(funcs, func(i, j int) bool {
		if funcs[i].complexity != funcs[j].complexity {
			return funcs[i].complexity > funcs[j].complexity
		}
		return funcs[i].lines > funcs[j].lines
	})

	// Cross-file coupling: count identifier uses of declarations owned by
	// another file. Selector fields and local variables can produce false
	// positives; treat the ranking as a review signal, not a proof.
	edgeCounts := make(map[string]map[string]int)
	for _, info := range files {
		ast.Inspect(info.ast, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			owner, ok := declOwner[ident.Name]
			if !ok || owner == info.path {
				return true
			}
			if edgeCounts[info.path] == nil {
				edgeCounts[info.path] = make(map[string]int)
			}
			edgeCounts[info.path][owner]++
			return true
		})
	}
	edges := make([]edge, 0)
	for from, tos := range edgeCounts {
		for to, count := range tos {
			edges = append(edges, edge{from: from, to: to, count: count})
		}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].count > edges[j].count })

	var totalLines, totalFuncs, testFiles int
	var totalBytes int64
	for _, info := range files {
		totalLines += info.lines
		totalBytes += info.bytes
		totalFuncs += info.funcs
		if info.test {
			testFiles++
		}
	}

	fmt.Printf("## Package summary\n\n")
	fmt.Printf("- Directory: `%s`\n", *dir)
	fmt.Printf("- Go files: %d (%d test, %d non-test)\n", len(files), testFiles, len(files)-testFiles)
	fmt.Printf("- Lines: %d; bytes: %d (%.1f KiB)\n", totalLines, totalBytes, float64(totalBytes)/1024)
	fmt.Printf("- Top-level functions: %d\n\n", totalFuncs)

	sortedFiles := append([]*fileInfo(nil), files...)
	sort.Slice(sortedFiles, func(i, j int) bool { return sortedFiles[i].lines > sortedFiles[j].lines })
	fmt.Printf("## Largest files\n\n| File | Lines | KiB | Funcs |\n|---|---:|---:|---:|\n")
	largest := 15
	if len(sortedFiles) < largest {
		largest = len(sortedFiles)
	}
	for _, info := range sortedFiles[:largest] {
		fmt.Printf("| `%s` | %d | %.1f | %d |\n", filepath.Base(info.path), info.lines, float64(info.bytes)/1024, info.funcs)
	}

	fmt.Printf("\n## Highest-complexity functions (branch-count heuristic)\n\n| Function | Location | Lines | Complexity |\n|---|---|---:|---:|\n")
	for i := 0; i < len(funcs) && i < *topFuncs; i++ {
		fmt.Printf("| `%s` | `%s:%d` | %d | %d |\n", funcs[i].name, filepath.Base(funcs[i].file), funcs[i].line, funcs[i].lines, funcs[i].complexity)
	}

	fmt.Printf("\n## Strongest cross-file references\n\n| From | To | Identifier uses |\n|---|---|---:|\n")
	for i := 0; i < len(edges) && i < *topEdges; i++ {
		fmt.Printf("| `%s` | `%s` | %d |\n", filepath.Base(edges[i].from), filepath.Base(edges[i].to), edges[i].count)
	}

	fanOut := make([]edge, 0, len(files))
	for _, info := range files {
		total := 0
		for _, count := range edgeCounts[info.path] {
			total += count
		}
		fanOut = append(fanOut, edge{from: info.path, count: total})
	}
	sort.Slice(fanOut, func(i, j int) bool { return fanOut[i].count > fanOut[j].count })
	fmt.Printf("\n## Files with the widest cross-file fan-out\n\n| File | Outgoing identifier uses |\n|---|---:|\n")
	for i := 0; i < len(fanOut) && i < 12; i++ {
		fmt.Printf("| `%s` | %d |\n", filepath.Base(fanOut[i].from), fanOut[i].count)
	}

	type fanInInfo struct {
		path  string
		count int
		files int
	}
	fanInCounts := make(map[string]int)
	fanInFiles := make(map[string]map[string]struct{})
	for _, e := range edges {
		fanInCounts[e.to] += e.count
		if fanInFiles[e.to] == nil {
			fanInFiles[e.to] = make(map[string]struct{})
		}
		fanInFiles[e.to][e.from] = struct{}{}
	}
	fanIn := make([]fanInInfo, 0, len(fanInCounts))
	for path, count := range fanInCounts {
		fanIn = append(fanIn, fanInInfo{path: path, count: count, files: len(fanInFiles[path])})
	}
	sort.Slice(fanIn, func(i, j int) bool { return fanIn[i].count > fanIn[j].count })
	fmt.Printf("\n## Files with the widest cross-file fan-in\n\n| File | Incoming identifier uses | Referencing files |\n|---|---:|---:|\n")
	for i := 0; i < len(fanIn) && i < 12; i++ {
		fmt.Printf("| `%s` | %d | %d |\n", filepath.Base(fanIn[i].path), fanIn[i].count, fanIn[i].files)
	}

	fmt.Printf("\n## Functions in `%s`\n\n| Function | Line | Lines | Complexity |\n|---|---:|---:|---:|\n", *focus)
	focusFuncs := make([]funcInfo, 0)
	for _, fn := range funcs {
		if filepath.Base(fn.file) == *focus {
			focusFuncs = append(focusFuncs, fn)
		}
	}
	sort.Slice(focusFuncs, func(i, j int) bool { return focusFuncs[i].line < focusFuncs[j].line })
	for _, fn := range focusFuncs {
		fmt.Printf("| `%s` | %d | %d | %d |\n", fn.name, fn.line, fn.lines, fn.complexity)
	}
}

func receiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return receiverName(e.X)
	}
	return "?"
}

func complexity(body *ast.BlockStmt) int {
	score := 1
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.CaseClause, *ast.CommClause:
			score++
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				score++
			}
		}
		return true
	})
	return score
}
