//go:build unix

package auth

import (
	"os"
	"path/filepath"
	"syscall"
)

func validateAuthStateLockDirectory(root *os.Root, directoryPath string) error {
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return authStateLockPathError(ErrAuthStateLockUnsafeDirectory, directoryPath)
	}
	if !isTrustedAuthStateLockOwner(info) {
		return authStateLockPathError(ErrAuthStateLockUnsafeAncestor, directoryPath)
	}
	if err := validateAuthStateLockRootIdentity(info, directoryPath); err != nil {
		return err
	}

	currentPath := filepath.Clean(directoryPath)
	childInfo := info
	for parentPath := filepath.Dir(currentPath); parentPath != currentPath; parentPath = filepath.Dir(currentPath) {
		parentInfo, err := os.Lstat(parentPath)
		if err != nil {
			return err
		}
		if !parentInfo.IsDir() || !isTrustedAuthStateLockOwner(parentInfo) {
			return authStateLockPathError(ErrAuthStateLockUnsafeAncestor, parentPath)
		}
		if parentInfo.Mode().Perm()&0o022 != 0 {
			if parentInfo.Mode()&os.ModeSticky == 0 || !isTrustedAuthStateLockOwner(childInfo) {
				return authStateLockPathError(ErrAuthStateLockUnsafeAncestor, parentPath)
			}
		}
		currentPath = parentPath
		childInfo = parentInfo
	}
	return validateAuthStateLockRootIdentity(info, directoryPath)
}

func isTrustedAuthStateLockOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	uid := uint32(os.Geteuid())
	return stat.Uid == 0 || stat.Uid == uid
}

func validateAuthStateLockRootIdentity(rootInfo os.FileInfo, directoryPath string) error {
	pathInfo, err := os.Lstat(directoryPath)
	if err != nil {
		return err
	}
	if !pathInfo.IsDir() || !os.SameFile(rootInfo, pathInfo) {
		return authStateLockPathError(ErrAuthStateLockUnsafeAncestor, directoryPath)
	}
	return nil
}
