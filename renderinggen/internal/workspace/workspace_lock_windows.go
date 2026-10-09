//go:build windows

package workspace

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const workspaceStillActive = 259

var workspaceHeldLocks sync.Map // map[*os.File]windows.Handle

func lockWorkspaceSlot(file *os.File, nonblocking bool) (bool, error) {
	name, err := windows.UTF16PtrFromString("Local\\RenderingGen-" + file.Name())
	if err != nil {
		return false, err
	}
	handle, err := windows.CreateMutexEx(nil, name, 0, windows.SYNCHRONIZE|windows.MUTEX_MODIFY_STATE)
	if err != nil {
		return false, err
	}
	timeout := uint32(windows.INFINITE)
	if nonblocking {
		timeout = 0
	}
	result, err := windows.WaitForSingleObject(handle, timeout)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return false, err
	}
	switch result {
	case uint32(windows.WAIT_OBJECT_0), uint32(windows.WAIT_ABANDONED):
		workspaceHeldLocks.Store(file, handle)
		return true, nil
	case uint32(windows.WAIT_TIMEOUT):
		_ = windows.CloseHandle(handle)
		return false, nil
	default:
		_ = windows.CloseHandle(handle)
		return false, windows.ERROR_INVALID_FUNCTION
	}
}

func unlockWorkspaceSlot(file *os.File) error {
	value, ok := workspaceHeldLocks.LoadAndDelete(file)
	if !ok {
		return os.ErrPermission
	}
	handle := value.(windows.Handle)
	releaseErr := windows.ReleaseMutex(handle)
	closeErr := windows.CloseHandle(handle)
	if releaseErr != nil {
		return releaseErr
	}
	return closeErr
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	if uint32(pid) == windows.GetCurrentProcessId() {
		return true
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied is not evidence that a process is absent.
		return err == windows.ERROR_ACCESS_DENIED || err != windows.ERROR_INVALID_PARAMETER
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == workspaceStillActive
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
	if err != nil || pid <= 0 || processExists(pid) {
		return false
	}
	stale := marker + ".stale-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.Rename(marker, stale); err != nil {
		return false
	}
	_ = os.Remove(stale)
	return true
}
