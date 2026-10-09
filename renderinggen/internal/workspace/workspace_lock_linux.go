//go:build linux

package workspace

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockWorkspaceSlot(file *os.File, nonblocking bool) (bool, error) {
	flags := unix.LOCK_EX
	if nonblocking {
		flags |= unix.LOCK_NB
	}
	if err := unix.Flock(int(file.Fd()), flags); err != nil {
		if nonblocking && (err == unix.EWOULDBLOCK || err == unix.EAGAIN) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func unlockWorkspaceSlot(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return err
	}
	return nil
}
