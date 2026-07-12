//go:build !linux

package hostcheck

import (
	"context"
	"errors"
)

func Collect(_ context.Context, _ string) (Facts, error) {
	return Facts{}, errors.New("host probes require Linux")
}
