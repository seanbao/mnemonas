//go:build !unix

package backup

import (
	"fmt"
	"os"
)

func validateBackupStateLockDirectory(root *os.Root, directoryPath string) error {
	rootInfo, err := root.Stat(".")
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(directoryPath)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() || !pathInfo.IsDir() || !os.SameFile(rootInfo, pathInfo) {
		return fmt.Errorf("%w: %s", ErrBackupStateLockUnsafeAncestor, directoryPath)
	}
	return nil
}
