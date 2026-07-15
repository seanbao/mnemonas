//go:build !unix

package storage

import "os"

func tryLockStorageRootDirectory(*os.File) error {
	return ErrStorageRootLockUnsupported
}

func unlockStorageRootDirectory(*os.File) error {
	return nil
}
