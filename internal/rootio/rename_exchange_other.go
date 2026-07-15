//go:build unix && !linux && !darwin

package rootio

import "golang.org/x/sys/unix"

func exchangeAt(firstDirFD int, firstName string, secondDirFD int, secondName string) error {
	return unix.ENOTSUP
}
