package main

// Analyzer: prints per-decl stats (byte offsets + line sizes) for Go files
// given on stdin (one path per line). Stdlib only; used to plan splits.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"sort"
	"strings"
)

func declDoc(d ast.Decl) *ast.CommentGroup {
	switch v := d.(type) {
	case *ast.FuncDecl:
		return v.Doc
	case *ast.GenDecl:
		return v.Doc
	}
	return nil
}

func analyze(path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("ERR", path, err)
		return
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		fmt.Println("ERR", path, err)
		return
	}
	pkgOff := fset.Position(f.Package).Offset
	headerLines := bytes.Count(src[:pkgOff], []byte("\n"))
	total := bytes.Count(src, []byte("\n"))

	fmt.Printf("=== %s total=%d header=%d imports=%d\n", path, total, headerLines, len(f.Imports))

	type decl struct {
		kind, name string
		s, e, l    int
	}
	var decls []decl
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		start := fset.Position(d.Pos()).Offset
		if doc := declDoc(d); doc != nil {
			start = fset.Position(doc.Pos()).Offset
		}
		end := fset.Position(d.End()).Offset
		dd := decl{s: start, e: end}
		switch v := d.(type) {
		case *ast.FuncDecl:
			dd.kind, dd.name = "func", v.Name.Name
		case *ast.GenDecl:
			dd.kind = v.Tok.String()
			if len(v.Specs) > 0 {
				if vs, ok := v.Specs[0].(*ast.ValueSpec); ok && len(vs.Names) > 0 {
					dd.name = vs.Names[0].Name
				}
				if ts, ok := v.Specs[0].(*ast.TypeSpec); ok {
					dd.name = ts.Name.Name
				}
			}
			if len(v.Specs) > 1 {
				dd.name = fmt.Sprintf("%s (%d specs)", dd.name, len(v.Specs))
			}
		default:
			dd.kind = fmt.Sprintf("%T", d)
		}
		dd.l = bytes.Count(src[start:end], []byte("\n")) + 1
		decls = append(decls, dd)
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].l > decls[j].l })
	var tot int
	for _, dd := range decls {
		tot += dd.l
	}
	fmt.Printf("    decls=%d totalDeclLines=%d\n", len(decls), tot)
	for i, dd := range decls {
		flag := ""
		if dd.l > 250 {
			flag = "  <-- OVER 250"
		}
		fmt.Printf("    %3d. %5d  %-6s %s (decl at %d..%d)%s\n", i+1, dd.l, dd.kind, dd.name, dd.s, dd.e, flag)
	}
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			analyze(line)
		}
	}
}
