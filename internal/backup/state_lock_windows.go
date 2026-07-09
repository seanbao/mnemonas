//go:build windows

package backup

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const backupStateLockBytes = 1

func tryLockBackupStateFile(file *os.File) error {
	overlapped := &windows.Overlapped{}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		backupStateLockBytes,
		0,
		overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return ErrBackupStateLockHeld
	}
	return err
}

func unlockBackupStateFile(file *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, backupStateLockBytes, 0, overlapped)
}
