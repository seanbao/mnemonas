//go:build windows

package auth

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const authStateLockBytes = 1

func tryLockAuthStateFile(file *os.File) error {
	overlapped := &windows.Overlapped{}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		authStateLockBytes,
		0,
		overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return ErrAuthStateLockHeld
	}
	return err
}

func unlockAuthStateFile(file *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, authStateLockBytes, 0, overlapped)
}
