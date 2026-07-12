//go:build !linux || !amd64 || !cgo

package containerengine

import "context"

type Engine struct{}

func Open(context.Context, Config) (*Engine, error) {
	return nil, ErrUnsupported
}

func (e *Engine) Close() error {
	return nil
}
