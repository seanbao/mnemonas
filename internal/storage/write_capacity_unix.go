//go:build unix

package storage

import (
	"errors"
	"syscall"
)

func isWriteStorageCapacityError(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}
