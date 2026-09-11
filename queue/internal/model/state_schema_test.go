package model

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/migrations"
)

// latestCheck extracts the value list of the most recent
// `ADD CONSTRAINT <name> CHECK (<col> IN (...))` across the migration files,
// in lexicographic (apply) order, so the assertion always reads the schema the
// migrator would end up with.
func latestCheck(t *testing.T, constraint string) []string {
	t.Helper()
	names, err := migrations.Names()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	re := regexp.MustCompile(`(?s)ADD CONSTRAINT\s+` + regexp.QuoteMeta(constraint) + `\s+CHECK\s*\([^()]*?IN\s*\(([^)]*)\)`)
	var values []string
	for _, name := range names {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		m := re.FindSubmatch(raw)
		if m == nil {
			continue
		}
		values = values[:0]
		for _, part := range strings.Split(string(m[1]), ",") {
			v := strings.TrimSpace(part)
			v = strings.Trim(v, "'\"")
			if v != "" {
				values = append(values, v)
			}
		}
	}
	if len(values) == 0 {
		t.Fatalf("no migration defines constraint %q", constraint)
	}
	return values
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// TestJobStateVocabularyMatchesSQLConstraint is the cross-layer pin for the
// job lifecycle states: the Go vocabulary (canonical, defined once in
// queue/client and re-exported here) and the PostgreSQL CHECK constraint must
// list exactly the same states. Without this test, adding a state to Go but
// not to SQL (or vice versa) is a silent runtime failure that only surfaces
// when a job transition is rejected by the database.
func TestJobStateVocabularyMatchesSQLConstraint(t *testing.T) {
	want := make([]string, 0, 8)
	for _, s := range States() {
		want = append(want, string(s))
	}
	got := latestCheck(t, "render_jobs_state_check")
	if strings.Join(sortedStrings(got), ",") != strings.Join(sortedStrings(want), ",") {
		t.Fatalf("render_jobs_state_check %v != Go States() %v", sortedStrings(got), sortedStrings(want))
	}
}

// TestAttemptStatusVocabularyMatchesSQLConstraint does the same for the
// render_attempts status vocabulary.
func TestAttemptStatusVocabularyMatchesSQLConstraint(t *testing.T) {
	want := make([]string, 0, 8)
	for _, s := range AttemptStatuses() {
		want = append(want, string(s))
	}
	got := latestCheck(t, "render_attempts_status_check")
	if strings.Join(sortedStrings(got), ",") != strings.Join(sortedStrings(want), ",") {
		t.Fatalf("render_attempts_status_check %v != Go AttemptStatuses() %v", sortedStrings(got), sortedStrings(want))
	}
}

// TestStateAliasesMatchClientType pins that the internal aliases are the
// canonical wire values, not a second definition that happens to agree today.
func TestStateAliasesMatchClientType(t *testing.T) {
	pairs := map[string]struct{ alias, canonical string }{
		"pending":    {string(StatePending), "pending"},
		"running":    {string(StateRunning), "running"},
		"finalizing": {string(StateFinalizing), "finalizing"},
		"completed":  {string(StateCompleted), "completed"},
		"failed":     {string(StateFailed), "failed"},
		"cancelled":  {string(StateCancelled), "cancelled"},
		"rendered":   {string(StateRendered), "rendered"},
	}
	for name, p := range pairs {
		if p.alias != p.canonical {
			t.Errorf("%s: alias %q != canonical %q", name, p.alias, p.canonical)
		}
	}
}
