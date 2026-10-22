//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package workspace

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// reclaimStaleMarker removes a marker whose owning process is confirmed absent
// on this host. It is shared by the exotic-GOOS and windows fallbacks: both
// serialize the advisory lock with the same sibling marker file, so the
// reclamation rule must be identical. processExists supplies the platform
// probe and is deliberately conservative.
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
