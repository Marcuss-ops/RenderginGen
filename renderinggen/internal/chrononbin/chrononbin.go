// Package chrononbin owns the install-prefix and binary-path resolution shared
// by the production Chronon client and the test gating helpers.
//
// It deliberately depends on nothing but the standard library: internal/chronon
// imports it, and same-package chronon tests import internal/testenv, so a
// shared helper that lived in chronon would create an import cycle.
package chrononbin

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultHome is the runtime image's install prefix, used when neither the
// caller nor the environment names one.
const DefaultHome = "/opt/chronon3d"

// Home returns the configured install prefix, or DefaultHome.
func Home(override string) string {
	if home := strings.TrimSpace(override); home != "" {
		return home
	}
	return DefaultHome
}

// Resolve returns the engine binary selected by, in order:
//
//  1. the CHRONON_BINARY override passed by the caller;
//  2. the client's explicit BinaryPath;
//  3. $HOME/bin/chronon3d_cli;
//  4. $HOME/apps/chronon3d_cli/chronon3d_cli.
//
// When none exists, the conventional $HOME/bin/chronon3d_cli path is returned
// so a startup error still names the configured prefix.
func Resolve(home, binaryPath, override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	if binaryPath = strings.TrimSpace(binaryPath); binaryPath != "" {
		return binaryPath
	}
	home = Home(home)
	for _, candidate := range []string{
		filepath.Join(home, "bin", "chronon3d_cli"),
		filepath.Join(home, "apps", "chronon3d_cli", "chronon3d_cli"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(home, "bin", "chronon3d_cli")
}
