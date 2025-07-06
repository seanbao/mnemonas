//go:build unix

package backup

import (
	"errors"
	"syscall"
)

var errRestoreAvailableInvalidBlockSize = errors.New("filesystem reported invalid block size")

func restoreAvailableBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return restoreAvailableBytesFromStatfs(uint64(stat.Bavail), int64(stat.Bsize))
}

func restoreAvailableBytesFromStatfs(availableBlocks uint64, blockSize int64) (int64, error) {
	if blockSize <= 0 {
		return 0, errRestoreAvailableInvalidBlockSize
	}

	const maxInt64 = uint64(1<<63 - 1)
	blockSizeBytes := uint64(blockSize)
	if availableBlocks > maxInt64/blockSizeBytes {
		return int64(maxInt64), nil
	}
	available := availableBlocks * blockSizeBytes
	return int64(available), nil
}
