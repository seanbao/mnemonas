//go:build !unix

package storage

func isWriteStorageCapacityError(error) bool {
	return false
}
