//go:build !linux

package diskusage

import "io/fs"

type fileIdentity struct{}

func deviceOf(fs.FileInfo) (uint64, bool) {
	return 0, false
}

func identityOf(fs.FileInfo) (fileIdentity, bool) {
	return fileIdentity{}, false
}

func allocatedBytes(info fs.FileInfo) uint64 {
	return uint64(max(info.Size(), 0))
}
