//go:build linux

package workspace

import (
	"os"
	"syscall"
)

func platformDeleteIdentity(info os.FileInfo) (platformFileIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return platformFileIdentity{}, false
	}
	return platformFileIdentity{
		deviceID: uint64(stat.Dev),
		inode:    stat.Ino,
		ctimeSec: stat.Ctim.Sec,
		ctimeNS:  stat.Ctim.Nsec,
	}, true
}
