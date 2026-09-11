package architecture

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Baseline is the ratchet ledger of pre-existing violations. It may only
// shrink: the conformance test fails on a new violation AND on a stale entry.
type Baseline struct {
	path    string
	entries map[string]bool // "rule|file"
}

// Key is the stable baseline identity for a violation (per rule, per file).
func (v Violation) Key() string { return v.Rule + "|" + v.File }

// LoadBaseline reads a baseline file. A missing file yields an empty baseline
// (no carve-out).
func LoadBaseline(path string) (*Baseline, error) {
	b := &Baseline{path: path, entries: map[string]bool{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return b, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "|") {
			return nil, fmt.Errorf("%s:%d: baseline entry must be rule|file", path, lineNo)
		}
		b.entries[line] = true
	}
	return b, sc.Err()
}

// Split partitions violations into new (must fail) and suppressed (baselined),
// and reports which baseline entries were not matched (stale → must fail).
//
// exists resolves a baseline path to disk; an entry whose file does not exist
// (for example a sibling repository that is not checked out) is ignored rather
// than reported stale, so the ratchet is correct in a standalone RenderingGen
// checkout and stricter in the full workspace.
func (b *Baseline) Split(vs []Violation, exists func(string) bool) (newViolations []Violation, stale []string) {
	used := map[string]bool{}
	for _, v := range vs {
		k := v.Key()
		if b.entries[k] {
			used[k] = true
			continue
		}
		newViolations = append(newViolations, v)
	}
	for k := range b.entries {
		if used[k] {
			continue
		}
		file := k
		if i := strings.Index(k, "|"); i >= 0 {
			file = k[i+1:]
		}
		if exists != nil && !exists(file) {
			continue
		}
		stale = append(stale, k)
	}
	sort.Strings(stale)
	return newViolations, stale
}

// Write rewrites the baseline from the current violation set, sorted and
// de-duplicated. This is the explicit ratchet-down operation.
func (b *Baseline) Write(vs []Violation) error {
	keys := map[string]bool{}
	for _, v := range vs {
		keys[v.Key()] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	var sb strings.Builder
	sb.WriteString("# Architecture conformance baseline (ratchet — may only shrink).\n")
	sb.WriteString("# Format: <rule>|<workspace-relative-file>. Regenerate explicitly with\n")
	sb.WriteString("# UPDATE_CONFORMANCE_BASELINE=1 go test ./internal/architecture/...\n")
	for _, k := range ordered {
		sb.WriteString(k)
		sb.WriteByte('\n')
	}
	if err := os.MkdirAll(dirOf(b.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(b.path, []byte(sb.String()), 0o644)
}

func dirOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "."
	}
	return p[:i]
}
