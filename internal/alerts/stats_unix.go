//go:build unix

package alerts

import (
	"fmt"
	"syscall"
)

func (m *Monitor) getStats() (*StorageStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(m.dataDir, &stat); err != nil {
		return nil, fmt.Errorf("statfs failed: %w", err)
	}

	stats, err := storageStatsFromStatfsBlocks(m.dataDir, uint64(stat.Blocks), uint64(stat.Bavail), int64(stat.Bsize), m.currentTime())
	if err != nil {
		return nil, err
	}
	return &stats, nil
}
