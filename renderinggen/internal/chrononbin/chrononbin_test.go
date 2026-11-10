package chrononbin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeDefaultAndOverride(t *testing.T) {
	if got := Home(""); got != DefaultHome {
		t.Fatalf("Home(\"\") = %q, want %q", got, DefaultHome)
	}
	if got := Home(" /tmp/chronon "); got != "/tmp/chronon" {
		t.Fatalf("Home(override) = %q", got)
	}
}

func TestResolvePrecedence(t *testing.T) {
	if got := Resolve("/home/default", "/explicit/bin", "/override/bin"); got != "/override/bin" {
		t.Fatalf("override lost: %q", got)
	}
	if got := Resolve("/home/default", "/explicit/bin", ""); got != "/explicit/bin" {
		t.Fatalf("BinaryPath lost: %q", got)
	}
}

func TestResolveFindsBothInstallLayouts(t *testing.T) {
	for _, rel := range []string{
		filepath.Join("bin", "chronon3d_cli"),
		filepath.Join("apps", "chronon3d_cli", "chronon3d_cli"),
	} {
		home := t.TempDir()
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := Resolve(home, "", ""); got != path {
			t.Fatalf("Resolve(%q) = %q, want %q", rel, got, path)
		}
	}
}

func TestResolveFallsBackToConventionalPath(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "bin", "chronon3d_cli")
	if got := Resolve(home, "", ""); got != want {
		t.Fatalf("fallback = %q, want %q", got, want)
	}
}
