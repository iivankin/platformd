//go:build !linux || !amd64 || !cgo

package cloudflaremesh

import (
	"context"
	"errors"
)

type unsupportedRuntime struct{}

func ProductionRuntime(config ProductionRuntimeConfig) (Runtime, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return unsupportedRuntime{}, nil
}

func (unsupportedRuntime) Ensure(context.Context, string, bool) error {
	return errors.New("managed Cloudflare Mesh is available only on Linux")
}

func (unsupportedRuntime) Address() (NetworkAddress, error) {
	return NetworkAddress{}, errors.New("managed Cloudflare Mesh is available only on Linux")
}

func (unsupportedRuntime) Close() error { return nil }
