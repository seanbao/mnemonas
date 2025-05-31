//go:build unix

package alerts

import (
	"fmt"
	"syscall"
	"time"
)

func (m *Monitor) getStats() (*StorageStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(m.dataDir, &stat); err != nil {
		return nil, fmt.Errorf("statfs failed: %w", err)
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bavail * uint64(stat.Bsize)
	usedBytes := totalBytes - freeBytes
	usedPct := float64(usedBytes) / float64(totalBytes) * 100

	return &StorageStats{
		Path:       m.dataDir,
		TotalBytes: totalBytes,
		FreeBytes:  freeBytes,
		UsedBytes:  usedBytes,
		UsedPct:    usedPct,
		CheckedAt:  time.Now(),
	}, nil
}
