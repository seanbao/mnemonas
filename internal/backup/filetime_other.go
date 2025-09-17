//go:build !unix

package backup

import (
	"os"
	"time"
)

func setOpenFileTimes(_ *os.File, path string, modTime time.Time) error {
	return os.Chtimes(path, modTime, modTime)
}
