//go:build unix

package storage

import (
	"errors"
	"syscall"
)

func diskStatsForHostPath(root string) (*DiskStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		return nil, err
	}
	if stat.Bsize <= 0 {
		return nil, errors.New("filesystem reported invalid block size")
	}

	blockSize := uint64(stat.Bsize)
	totalBytes := stat.Blocks * blockSize
	freeBytes := stat.Bfree * blockSize
	availableBytes := stat.Bavail * blockSize
	usedBytes := uint64(0)
	if totalBytes > freeBytes {
		usedBytes = totalBytes - freeBytes
	}

	usageRatio := 0.0
	if totalBytes > 0 {
		usageRatio = float64(usedBytes) / float64(totalBytes)
	}

	fsType := filesystemTypeForPath(root, uint64(stat.Type))
	return &DiskStats{
		TotalBytes:                totalBytes,
		FreeBytes:                 freeBytes,
		AvailableBytes:            availableBytes,
		UsedBytes:                 usedBytes,
		UsageRatio:                usageRatio,
		FileSystemType:            fsType,
		NativeDataChecksumSupport: filesystemHasNativeDataChecksumSupport(fsType),
	}, nil
}
