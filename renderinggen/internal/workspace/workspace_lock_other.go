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

func reclaimStaleMarker(marker string) bool {
	data, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return false
	}
	if processExists(pid) {
		return false
	}
	stale := marker + ".stale-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.Rename(marker, stale); err != nil {
		return false
	}
	_ = os.Remove(stale)
	return true
}
