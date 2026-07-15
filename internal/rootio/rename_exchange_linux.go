//go:build linux

package rootio

import "golang.org/x/sys/unix"

func exchangeAt(firstDirFD int, firstName string, secondDirFD int, secondName string) error {
	return unix.Renameat2(firstDirFD, firstName, secondDirFD, secondName, unix.RENAME_EXCHANGE)
}
