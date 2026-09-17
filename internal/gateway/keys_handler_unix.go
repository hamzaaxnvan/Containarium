//go:build !windows

package gateway

import (
	"os"
	"syscall"
)

type fileOwnerInfo struct {
	uid int
	gid int
}

func fileOwner(info os.FileInfo) (fileOwnerInfo, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileOwnerInfo{}, false
	}
	return fileOwnerInfo{uid: int(stat.Uid), gid: int(stat.Gid)}, true
}
