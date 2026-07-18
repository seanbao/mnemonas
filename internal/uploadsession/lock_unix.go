//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package uploadsession

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockStoreFile(file *os.File) error {
	if file == nil {
		return errors.New("upload session lock file is unavailable")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("lock upload session store: %w", err)
	}
	return nil
}

func unlockStoreFile(file *os.File) error {
	if file == nil {
		return nil
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("unlock upload session store: %w", err)
	}
	return nil
}
