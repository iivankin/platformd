//go:build !linux || !amd64 || !cgo

package containerengine

import "context"

func (*Engine) RemoveManagedVolume(context.Context, string) error {
	return ErrUnsupported
}
