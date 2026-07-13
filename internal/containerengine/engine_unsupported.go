//go:build !linux || !amd64 || !cgo

package containerengine

import "context"

type Engine struct {
	logs logActivity
}

func Open(context.Context, Config) (*Engine, error) {
	return nil, ErrUnsupported
}

func (e *Engine) Close() error {
	return nil
}

func (e *Engine) CloseForUpdate() error {
	return nil
}
