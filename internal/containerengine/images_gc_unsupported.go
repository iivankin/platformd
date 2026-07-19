//go:build !linux || !amd64 || !cgo

package containerengine

import "context"

func (*Engine) GarbageCollectImages(context.Context, ImageGarbageCollectRequest) (ImageGarbageCollectResult, error) {
	return ImageGarbageCollectResult{}, ErrUnsupported
}
