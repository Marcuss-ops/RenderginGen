//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !windows

package workspace

import "os"

// processExists conservatively treats a marker as live when the platform has
// no portable process-presence probe in this package. This may defer cleanup,
// but never reclaims a marker owned by a live process based on guesswork.
func processExists(pid int) bool {
	return pid > 0 && pid == os.Getpid()
}
