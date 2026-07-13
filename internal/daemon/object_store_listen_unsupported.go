//go:build !linux

package daemon

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

func listenObjectStore(context.Context, netip.Addr, uint16) (net.Listener, error) {
	return nil, errors.New("project S3 IP_FREEBIND listener requires Linux")
}
