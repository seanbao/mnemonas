//go:build darwin

package backup

import (
	"fmt"
	"os"
	"syscall"
)

func readinessFileChangeToken(info os.FileInfo) (string, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d:%d", stat.Dev, stat.Ino, stat.Ctimespec.Sec, stat.Ctimespec.Nsec), true
}
