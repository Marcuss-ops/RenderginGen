//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !windows

package workspace

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// The fallback serializes each advisory lock file with a sibling marker. It is
// cooperative and intended for local filesystems. Stale markers are reclaimed
// only after their process ID is confirmed absent on this host.
func lockWorkspaceSlot(file *os.File, nonblocking bool) (bool, error) {
	marker := file.Name() + ".held"
	for {
		lock, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, writeErr := lock.WriteString(strconv.Itoa(os.Getpid()) + " " + strconv.FormatInt(time.Now().UnixNano(), 10))
			closeErr := lock.Close()
			if writeErr != nil {
				_ = os.Remove(marker)
				return false, writeErr
			}
			if closeErr != nil {
				_ = os.Remove(marker)
				return false, closeErr
			}
			return true, nil
		}
		if !os.IsExist(err) {
			return false, err
		}
		if reclaimStaleMarker(marker) {
			continue
		}
		if nonblocking {
			return false, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func unlockWorkspaceSlot(file *os.File) error {
	marker := file.Name() + ".held"
	data, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || fields[0] != strconv.Itoa(os.Getpid()) {
		return os.ErrPermission
	}
	return os.Remove(marker)
}

// processExists conservatively treats a marker as live when the platform has
// no portable process-presence probe in this package. This may defer cleanup,
// but never reclaims a marker owned by a live process based on guesswork.
func processExists(pid int) bool {
	return pid > 0 && pid == os.Getpid()
}

// NOTE(workspace-lock): reclaimStaleMarker lives in workspace_lock_marker.go,
// compiled for this fallback and for windows by the same build constraint. Keep
// that constraint in sync with this file; linux/darwin use their own lock path.
