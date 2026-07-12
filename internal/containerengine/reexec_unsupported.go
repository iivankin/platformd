//go:build !linux || !amd64 || !cgo

package containerengine

func InitReexec() bool {
	return false
}
