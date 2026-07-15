//go:build !unix

package storage

import (
	"errors"
	"os"
)

func diskStatsForOpenDirectory(*os.File, string) (*DiskStats, error) {
	return nil, errors.New("disk stats are not available on this platform")
}
