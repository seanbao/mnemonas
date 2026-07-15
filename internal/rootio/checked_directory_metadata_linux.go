//go:build linux

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
		ctimeSec:  stat.Ctim.Sec,
		ctimeNsec: stat.Ctim.Nsec,
	}, true
}
