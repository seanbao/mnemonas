//go:build unix

package auth

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockAuthStateFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrAuthStateLockHeld
	}
	return err
}

func unlockAuthStateFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
