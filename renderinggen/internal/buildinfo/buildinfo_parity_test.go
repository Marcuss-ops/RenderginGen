package buildinfo

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestIdentityWireParityWithObjectStore is the RenderingGen half of the
// cross-module buildinfo contract. The object store owns the forward check; this
// one proves the worker side still publishes the same field names and derives
// identity_hash from the same tuple, even though the two services deliberately
// keep separate Go modules.
func TestIdentityWireParityWithObjectStore(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	sibling := filepath.Join(repoRoot, "objectstore", "internal", "buildinfo", "buildinfo.go")
	source, err := os.ReadFile(sibling)
	if err != nil {
		t.Skipf("object store buildinfo is not checked out: %v", err)
	}
	text := string(source)

	want := localJSONKeys()
	got := jsonKeysOf(text)
	if len(got) == 0 {
		t.Fatal("could not parse the object store Identity JSON tags")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("identity wire keys diverged:\n worker: %v\n store:  %v", want, got)
	}
	local := filepath.Join(filepath.Dir(thisFile), "buildinfo.go")
	if got, want := digestTupleOf(text), digestTupleOf(readFile(t, local)); !reflect.DeepEqual(got, want) {
		t.Fatalf("identity_hash tuple diverged:\n worker: %v\n store:  %v", want, got)
	}
}

func localJSONKeys() []string {
	typ := reflect.TypeOf(Identity{})
	keys := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	return keys
}

func jsonKeysOf(source string) []string {
	start := strings.Index(source, "type Identity struct {")
	if start < 0 {
		return nil
	}
	body := source[start:]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	var keys []string
	for _, line := range strings.Split(body, "\n") {
		idx := strings.Index(line, `json:"`)
		if idx < 0 {
			continue
		}
		rest := line[idx+len(`json:"`):]
		end := strings.Index(rest, `"`)
		if end < 0 {
			continue
		}
		name := strings.Split(rest[:end], ",")[0]
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	return keys
}

var digestTuple = regexp.MustCompile(`(?s)strings\.Join\(\[\]string\{(.*?)\},\s*"\\x00"\)`)

func digestTupleOf(source string) []string {
	match := digestTuple.FindStringSubmatch(source)
	if len(match) < 2 {
		return nil
	}
	var fields []string
	for _, field := range regexp.MustCompile(`i\.[A-Za-z0-9_]+`).FindAllString(match[1], -1) {
		fields = append(fields, field)
	}
	return fields
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
