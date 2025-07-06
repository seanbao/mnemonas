//go:build unix

package storage

import "syscall"

func diskStatsForHostPath(root string) (*DiskStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		return nil, err
	}

	mountDetails := diskMountDetailsForPath(root, uint64(stat.Type))
	stats, err := diskStatsFromStatfsBlocks(uint64(stat.Blocks), uint64(stat.Bfree), uint64(stat.Bavail), int64(stat.Bsize), mountDetails)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}
