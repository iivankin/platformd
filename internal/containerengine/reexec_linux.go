//go:build linux && amd64 && cgo

package containerengine

import "go.podman.io/storage/pkg/reexec"

func InitReexec() bool {
	return reexec.Init()
}
