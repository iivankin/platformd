//go:build !linux || !amd64 || !cgo

package containerengine

import "context"

func (*Engine) Pull(context.Context, PullRequest) (Image, error) {
	return Image{}, ErrUnsupported
}

func (*Engine) InspectImage(context.Context, string) (Image, error) {
	return Image{}, ErrUnsupported
}

func (*Engine) CommitDerivedImage(context.Context, DerivedImageRequest) (Image, error) {
	return Image{}, ErrUnsupported
}

func (*Engine) ImagesByLabel(context.Context, string) ([]Image, error) {
	return nil, ErrUnsupported
}

func (*Engine) RemoveImage(context.Context, string) error {
	return ErrUnsupported
}
