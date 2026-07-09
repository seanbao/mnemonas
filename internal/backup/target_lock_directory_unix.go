//go:build unix

package backup

import "os"

func validateBackupTargetLockDirectorySecurity(root *os.Root, directoryPath string) error {
	return validateUnixBackupLockDirectory(
		root,
		directoryPath,
		ErrBackupTargetLockUnsafeDirectory,
		ErrBackupTargetLockUnsafeAncestor,
	)
}
