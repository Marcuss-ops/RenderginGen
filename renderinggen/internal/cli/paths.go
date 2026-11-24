// Package cli owns the path and binary resolvers shared by the small developer
// commands. The worker itself does not use these defaults: it receives explicit
// paths from its configuration.
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
)

// chrononCandidates are the checked-out Chronon build trees used by the demo
// commands, in release-first order. Test builds are deliberately last so a
// release binary wins when both exist.
var chrononCandidates = []string{
	"../../Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli",
	"../Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli",
	"Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli",
	"../../Chronon3d/build/chronon/linux-video-test/apps/chronon3d_cli/chronon3d_cli",
	"../Chronon3d/build/chronon/linux-video-test/apps/chronon3d_cli/chronon3d_cli",
	"Chronon3d/build/chronon/linux-video-test/apps/chronon3d_cli/chronon3d_cli",
}

var assetsRootCandidates = []string{
	"../../ChrononTemplate",
	"../ChrononTemplate",
	"ChrononTemplate",
}

// ResolveChronon returns explicit, CHRONON_BIN, the first existing sibling
// build, or the binary on PATH. An unresolved value is empty; the caller owns
// the user-facing error.
func ResolveChronon(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("CHRONON_BIN"); env != "" {
		return env
	}
	if path := firstExistingFile(chrononCandidates); path != "" {
		return path
	}
	if binary, err := exec.LookPath("chronon3d_cli"); err == nil {
		return binary
	}
	return ""
}

// ResolveAssetsRoot returns explicit, CHRONON_TEMPLATE_ROOT, or the first
// existing sibling ChrononTemplate checkout.
func ResolveAssetsRoot(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("CHRONON_TEMPLATE_ROOT"); env != "" {
		return env
	}
	return firstExistingDir(assetsRootCandidates)
}

func firstExistingFile(candidates []string) string {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		return absoluteOrOriginal(candidate)
	}
	return ""
}

func firstExistingDir(candidates []string) string {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		return absoluteOrOriginal(candidate)
	}
	return ""
}

func absoluteOrOriginal(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}
