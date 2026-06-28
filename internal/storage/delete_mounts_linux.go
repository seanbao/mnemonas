//go:build linux

package storage

import "os"

func currentDeleteMountPoints() ([]string, error) {
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return mountPointsFromMountInfo(mountInfo)
}
