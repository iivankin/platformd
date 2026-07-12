//go:build !linux || !amd64 || !cgo

package containerengine

import "context"

func (*Engine) Pull(context.Context, PullRequest) (Image, error) {
	return Image{}, ErrUnsupported
}

func (*Engine) InspectImage(context.Context, string) (Image, error) {
	return Image{}, ErrUnsupported
}
