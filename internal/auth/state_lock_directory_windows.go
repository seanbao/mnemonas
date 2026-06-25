//go:build windows

package auth

import "os"

func validateAuthStateLockDirectory(root *os.Root, directoryPath string) error {
	rootInfo, err := root.Stat(".")
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(directoryPath)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() || !pathInfo.IsDir() || !os.SameFile(rootInfo, pathInfo) {
		return authStateLockPathError(ErrAuthStateLockUnsafeAncestor, directoryPath)
	}
	return nil
}
