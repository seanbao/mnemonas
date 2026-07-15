//go:build unix

package storage

import (
	"os"
	"syscall"
)

func diskStatsForOpenDirectory(dir *os.File, displayPath string) (*DiskStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Fstatfs(int(dir.Fd()), &stat); err != nil {
		return nil, err
	}

	mountDetails := diskMountDetailsForPath(displayPath, uint64(stat.Type))
	stats, err := diskStatsFromStatfsBlocks(uint64(stat.Blocks), uint64(stat.Bfree), uint64(stat.Bavail), int64(stat.Bsize), mountDetails)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}
