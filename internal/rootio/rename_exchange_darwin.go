//go:build darwin

package rootio

import "golang.org/x/sys/unix"

func exchangeAt(firstDirFD int, firstName string, secondDirFD int, secondName string) error {
	return unix.RenameatxNp(
		firstDirFD,
		firstName,
		secondDirFD,
		secondName,
		unix.RENAME_SWAP|unix.RENAME_NOFOLLOW_ANY,
	)
}
