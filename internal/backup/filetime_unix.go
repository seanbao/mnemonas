//go:build unix

package backup

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func setOpenFileTimes(file *os.File, _ string, modTime time.Time) error {
	timeval := unix.NsecToTimeval(modTime.UnixNano())
	return unix.Futimes(int(file.Fd()), []unix.Timeval{timeval, timeval})
}
