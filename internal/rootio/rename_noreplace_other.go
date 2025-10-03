//go:build unix && !linux && !darwin

package rootio

import "golang.org/x/sys/unix"

func renameNoReplaceAt(oldDirFD int, oldName string, newDirFD int, newName string) error {
	return unix.ENOTSUP
}
