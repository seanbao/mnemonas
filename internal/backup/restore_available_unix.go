//go:build unix

package backup

import "syscall"

func restoreAvailableBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	const maxInt64 = uint64(1<<63 - 1)
	blockSize := uint64(stat.Bsize)
	if blockSize > 0 && uint64(stat.Bavail) > maxInt64/blockSize {
		return int64(maxInt64), nil
	}
	available := uint64(stat.Bavail) * blockSize
	if available > maxInt64 {
		return int64(maxInt64), nil
	}
	return int64(available), nil
}
