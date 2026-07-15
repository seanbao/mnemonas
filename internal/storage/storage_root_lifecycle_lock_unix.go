//go:build unix

package storage

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockStorageRootDirectory(dir *os.File) error {
	if dir == nil {
		return errStorageRootLockChanged
	}
	err := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
		return errors.Join(ErrStorageRootLockHeld, err)
	case errors.Is(err, unix.ENOTSUP),
		errors.Is(err, unix.EOPNOTSUPP),
		errors.Is(err, unix.ENOSYS):
		return errors.Join(ErrStorageRootLockUnsupported, err)
	default:
		return err
	}
}

func unlockStorageRootDirectory(dir *os.File) error {
	if dir == nil {
		return nil
	}
	return unix.Flock(int(dir.Fd()), unix.LOCK_UN)
}
