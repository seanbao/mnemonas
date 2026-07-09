//go:build !unix

package storage

import "os"

func removeCopiedMoveSource(root *os.Root, rel, _ string, expected os.FileInfo) error {
	current, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	if !sameStorageCopySource(expected, current) {
		return ErrDeleteTargetChanged
	}
	return root.Remove(rel)
}
