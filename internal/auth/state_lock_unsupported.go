//go:build !unix && !windows

package auth

import "os"

func tryLockAuthStateFile(_ *os.File) error {
	return ErrAuthStateLockUnsupported
}

func unlockAuthStateFile(_ *os.File) error {
	return nil
}
