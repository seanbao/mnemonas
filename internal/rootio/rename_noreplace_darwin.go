//go:build darwin

package rootio

import "golang.org/x/sys/unix"

func renameNoReplaceAt(oldDirFD int, oldName string, newDirFD int, newName string) error {
	return unix.RenameatxNp(oldDirFD, oldName, newDirFD, newName, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY)
}
