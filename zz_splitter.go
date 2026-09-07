package main

// zz_splitter: split Go source files into sequential parts of <=300 lines each.
// Reads file paths from stdin (one per line). Parts keep declaration order,
// recompute per-part imports, and keep the original file-level comment header
// only in the first part (build constraints are replicated everywhere).

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxLines = 300

type importInfo struct {
	path  string
	alias string // "" if none
	name  string // alias, or resolved default package name
	dot   bool
	blank bool
}

type declUnit struct {
	text []byte
	used map[int]bool
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var failed bool
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if err := splitFile(line); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR %s: %v\n", line, err)
			failed = true
		} else {
			fmt.Printf("OK    %s\n", line)
		}
	}
	if failed {
		os.Exit(1)
	}
}

var nameCache = map[string]string{}

func resolvePkgName(importPath string) string {
	if n, ok := nameCache[importPath]; ok {
		return n
	}
	out, err := exec.Command("go", "list", "-f", "{{.Name}}", importPath).Output()
	name := ""
	if err != nil {
		name = filepath.Base(importPath)
	} else {
		name = strings.TrimSpace(string(out))
	}
	nameCache[importPath] = name
	return name
}

func posOffset(fset *token.FileSet, p token.Pos) int {
	return fset.Position(p).Offset
}

func splitFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	pkgOff := posOffset(fset, f.Package)
	header := string(src[:pkgOff])
	var buildLines []string
	for _, ln := range strings.Split(header, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "//go:build ") || strings.HasPrefix(t, "// +build ") {
			buildLines = append(buildLines, ln)
		}
	}
	pkgName := f.Name.Name

	var imports []*importInfo
	nameToIdx := map[string]int{}
	var alwaysUsed []int
	for _, is := range f.Imports {
		p := strings.Trim(is.Path.Value, `"`)
		info := &importInfo{path: p}
		if is.Name != nil {
			info.alias = is.Name.Name
			if info.alias == "." {
				info.dot = true
			}
			if info.alias == "_" {
				info.blank = true
			}
		}
		if info.dot || info.blank {
			alwaysUsed = append(alwaysUsed, len(imports))
			imports = append(imports, info)
			continue
		}
		info.name = info.alias
		if info.name == "" {
			info.name = resolvePkgName(p)
		}
		if _, dup := nameToIdx[info.name]; dup {
			imports = append(imports, info)
			continue
		}
		nameToIdx[info.name] = len(imports)
		imports = append(imports, info)
	}

	type boundary struct {
		off  int
		decl ast.Decl
	}
	var bounds []boundary
	lastImportEnd := -1
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			e := posOffset(fset, gd.End())
			if e > lastImportEnd {
				lastImportEnd = e
			}
			continue
		}
		start := posOffset(fset, d.Pos())
		if doc := declDoc(d); doc != nil {
			start = posOffset(fset, doc.Pos())
		}
		bounds = append(bounds, boundary{off: start, decl: d})
	}
	if len(bounds) == 0 {
		return fmt.Errorf("no declarations to split")
	}
	for i := 1; i < len(bounds); i++ {
		if bounds[i].off < bounds[i-1].off {
			return fmt.Errorf("declaration boundaries out of order")
		}
	}
	cutStart := 0
	if lastImportEnd >= 0 {
		cutStart = lastImportEnd
	} else {
		cutStart = posOffset(fset, f.Name.End())
	}

	unitUsed := make([]map[int]bool, len(bounds))
	for i, b := range bounds {
		m := map[int]bool{}
		ast.Inspect(b.decl, func(n ast.Node) bool {
			se, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := se.X.(*ast.Ident)
			if !ok {
				return true
			}
			if idx, hit := nameToIdx[id.Name]; hit {
				m[idx] = true
			}
			return true
		})
		for _, a := range alwaysUsed {
			m[a] = true
		}
		unitUsed[i] = m
	}

	endOff := []int{cutStart}
	for _, b := range bounds {
		endOff = append(endOff, b.off)
	}
	endOff = append(endOff, len(src))

	units := make([]declUnit, 0, len(bounds))
	for i := range bounds {
		a, b := endOff[i+1], endOff[i+2]
		if b <= a {
			continue
		}
		units = append(units, declUnit{
			text: src[a:b],
			used: unitUsed[i],
		})
	}
	if len(units) == 0 {
		return fmt.Errorf("no units produced")
	}

	renderPart := func(first bool, unitIdxs []int) string {
		var b strings.Builder
		if first {
			b.WriteString(header)
		} else {
			for _, bl := range buildLines {
				b.WriteString(bl)
				b.WriteString("\n")
			}
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("package ")
		b.WriteString(pkgName)
		b.WriteString("\n")
		usedIdx := map[int]bool{}
		for _, ui := range unitIdxs {
			for k := range units[ui].used {
				usedIdx[k] = true
			}
		}
		var specStrs []string
		for i, imp := range imports {
			if !usedIdx[i] {
				continue
			}
			if imp.alias != "" {
				specStrs = append(specStrs, fmt.Sprintf("\t%s %q", imp.alias, imp.path))
			} else {
				specStrs = append(specStrs, fmt.Sprintf("\t%q", imp.path))
			}
		}
		if len(specStrs) > 0 {
			b.WriteString("\nimport (\n")
			b.WriteString(strings.Join(specStrs, "\n"))
			b.WriteString("\n)\n")
		}
		b.WriteString("\n")
		for _, ui := range unitIdxs {
			b.Write(units[ui].text)
		}
		out := b.String()
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return out
	}

	var groups [][]int
	i := 0
	for i < len(units) {
		first := len(groups) == 0
		j := i
		for j < len(units) {
			if curLines(renderPart(first, intRange(i, j+1))) <= maxLines {
				j++
				continue
			}
			break
		}
		if j == i {
			lines := curLines(renderPart(first, []int{i}))
			return fmt.Errorf("declaration %d too large (%d lines incl. overhead); needs manual refactor", i+1, lines)
		}
		groups = append(groups, intRange(i, j))
		i = j
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	var stem string
	isTest := strings.HasSuffix(base, "_test.go")
	if isTest {
		stem = strings.TrimSuffix(base, "_test.go")
	} else {
		stem = strings.TrimSuffix(base, ".go")
	}
	var outNames []string
	for gi := range groups {
		suffix := fmt.Sprintf("_part%02d", gi+1)
		var name string
		if isTest {
			name = stem + suffix + "_test.go"
		} else {
			name = stem + suffix + ".go"
		}
		outPath := filepath.Join(dir, name)
		if _, err := os.Stat(outPath); err == nil {
			return fmt.Errorf("target file already exists: %s", outPath)
		}
		outNames = append(outNames, outPath)
	}

	for gi, g := range groups {
		content := renderPart(gi == 0, g)
		lines := curLines(content)
		if lines > maxLines {
			return fmt.Errorf("internal: part %d has %d lines", gi+1, lines)
		}
		if err := os.WriteFile(outNames[gi], []byte(content), 0o644); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func curLines(s string) int {
	if s == "" {
		return 0
	}
	return bytes.Count([]byte(s), []byte("\n"))
}

func intRange(lo, hi int) []int {
	r := make([]int, 0, hi-lo)
	for i := lo; i < hi; i++ {
		r = append(r, i)
	}
	return r
}

func declDoc(d ast.Decl) *ast.CommentGroup {
	switch v := d.(type) {
	case *ast.FuncDecl:
		return v.Doc
	case *ast.GenDecl:
		return v.Doc
	}
	return nil
}
