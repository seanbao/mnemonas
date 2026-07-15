//go:build darwin

package rootio

import (
	"os"
	"syscall"
)

func platformCheckedDirectorySystemMetadata(info os.FileInfo) (checkedDirectorySystemMetadata, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return checkedDirectorySystemMetadata{}, false
	}
	return checkedDirectorySystemMetadata{
		uid:       uint64(stat.Uid),
		gid:       uint64(stat.Gid),
		nlink:     uint64(stat.Nlink),
		ctimeSec:  stat.Ctimespec.Sec,
		ctimeNsec: stat.Ctimespec.Nsec,
	}, true
}
