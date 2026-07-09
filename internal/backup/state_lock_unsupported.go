//go:build !unix && !windows

package backup

import "os"

func tryLockBackupStateFile(_ *os.File) error {
	return ErrBackupStateLockUnsupported
}

func unlockBackupStateFile(_ *os.File) error {
	return nil
}
