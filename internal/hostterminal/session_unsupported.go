//go:build !linux

package hostterminal

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/terminaltransport"
)

func spawnPTY(context.Context, spawnConfig) (terminaltransport.Session, error) {
	return nil, errors.New("host terminal requires Linux")
}
