//go:build unix

package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func validateBackupStateLockDirectory(root *os.Root, directoryPath string) error {
	return validateUnixBackupLockDirectory(
		root,
		directoryPath,
		ErrBackupStateLockUnsafeDirectory,
		ErrBackupStateLockUnsafeAncestor,
	)
}

func validateUnixBackupLockDirectory(root *os.Root, directoryPath string, unsafeDirectory, unsafeAncestor error) error {
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w: %s", unsafeDirectory, directoryPath)
	}
	if !isTrustedBackupStateLockOwner(info) {
		return fmt.Errorf("%w: %s", unsafeAncestor, directoryPath)
	}
	if err := validateUnixBackupLockRootIdentity(info, directoryPath, unsafeAncestor); err != nil {
		return err
	}

	currentPath := filepath.Clean(directoryPath)
	childInfo := info
	for parentPath := filepath.Dir(currentPath); parentPath != currentPath; parentPath = filepath.Dir(currentPath) {
		parentInfo, err := os.Lstat(parentPath)
		if err != nil {
			return err
		}
		if !parentInfo.IsDir() || !isTrustedBackupStateLockOwner(parentInfo) {
			return fmt.Errorf("%w: %s", unsafeAncestor, parentPath)
		}
		if parentInfo.Mode().Perm()&0o022 != 0 {
			if parentInfo.Mode()&os.ModeSticky == 0 || !isTrustedBackupStateLockOwner(childInfo) {
				return fmt.Errorf("%w: %s", unsafeAncestor, parentPath)
			}
		}
		currentPath = parentPath
		childInfo = parentInfo
	}
	return validateUnixBackupLockRootIdentity(info, directoryPath, unsafeAncestor)
}

func isTrustedBackupStateLockOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	uid := uint32(os.Geteuid())
	return stat.Uid == 0 || stat.Uid == uid
}

func validateUnixBackupLockRootIdentity(rootInfo os.FileInfo, directoryPath string, unsafeAncestor error) error {
	pathInfo, err := os.Lstat(directoryPath)
	if err != nil {
		return err
	}
	if !pathInfo.IsDir() || !os.SameFile(rootInfo, pathInfo) {
		return fmt.Errorf("%w: %s", unsafeAncestor, directoryPath)
	}
	return nil
}
