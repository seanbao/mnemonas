//go:build darwin

package storage

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func currentDeleteMountPoints() ([]string, error) {
	for attempts := 0; attempts < 4; attempts++ {
		count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		stats := make([]unix.Statfs_t, count+16)
		actual, err := unix.Getfsstat(stats, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		if actual >= len(stats) {
			continue
		}

		mountPoints := make([]string, 0, actual)
		for _, stat := range stats[:actual] {
			mountPoints = append(mountPoints, unix.ByteSliceToString(stat.Mntonname[:]))
		}
		return mountPoints, nil
	}
	return nil, fmt.Errorf("mount table changed during inspection")
}
