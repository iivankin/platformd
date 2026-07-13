//go:build unix

package volumestore

import (
	"os"
	"syscall"
)

func fileOwner(info os.FileInfo) (int, int) {
	stat := info.Sys().(*syscall.Stat_t)
	return int(stat.Uid), int(stat.Gid)
}
