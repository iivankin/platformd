//go:build linux

package diskusage

import (
	"io/fs"
	"syscall"
)

type fileIdentity struct {
	device uint64
	inode  uint64
}

func identityOf(info fs.FileInfo) (fileIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, false
	}
	return fileIdentity{device: stat.Dev, inode: stat.Ino}, true
}

func allocatedBytes(info fs.FileInfo) uint64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks < 0 {
		return uint64(max(info.Size(), 0))
	}
	return uint64(stat.Blocks) * 512
}
