//go:build !unix

package volumestore

import "os"

func fileOwner(_ os.FileInfo) (int, int) {
	return os.Geteuid(), os.Getegid()
}
