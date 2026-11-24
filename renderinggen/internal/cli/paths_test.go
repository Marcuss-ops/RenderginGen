package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveChrononExplicitAndEnv(t *testing.T) {
	const explicit = "/opt/chronon/custom"
	if got := ResolveChronon(explicit); got != explicit {
		t.Fatalf("ResolveChronon(explicit) = %q, want %q", got, explicit)
	}
	t.Setenv("CHRONON_BIN", "/opt/chronon/env")
	if got := ResolveChronon(""); got != "/opt/chronon/env" {
		t.Fatalf("ResolveChronon(env) = %q", got)
	}
}

func TestResolveAssetsRootExplicitAndEnv(t *testing.T) {
	root := t.TempDir()
	if got := ResolveAssetsRoot(root); got != root {
		t.Fatalf("ResolveAssetsRoot(explicit) = %q, want %q", got, root)
	}
	t.Setenv("CHRONON_TEMPLATE_ROOT", root)
	if got := ResolveAssetsRoot(""); got != root {
		t.Fatalf("ResolveAssetsRoot(env) = %q, want %q", got, root)
	}
}

func TestResolveAssetsRootDiscoversSiblingCheckout(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "ChrononTemplate"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent)
	t.Setenv("CHRONON_TEMPLATE_ROOT", "")
	want, err := filepath.Abs(filepath.Join(parent, "ChrononTemplate"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveAssetsRoot(""); got != want {
		t.Fatalf("ResolveAssetsRoot() = %q, want %q", got, want)
	}
}
