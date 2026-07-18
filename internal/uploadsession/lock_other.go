//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package uploadsession

import (
	"errors"
	"os"
)

func lockStoreFile(*os.File) error {
	return errors.New("upload session store locking is unsupported on this platform")
}

func unlockStoreFile(*os.File) error {
	return nil
}
