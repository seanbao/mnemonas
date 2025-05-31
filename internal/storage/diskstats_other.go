//go:build !unix

package storage

import "errors"

func diskStatsForHostPath(root string) (*DiskStats, error) {
	return nil, errors.New("disk stats are not available on this platform")
}
