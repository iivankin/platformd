//go:build !linux

package hosttunnel

import (
	"errors"
	"net"
	"net/netip"
)

func OriginalDestination(*net.TCPConn) (netip.AddrPort, error) {
	return netip.AddrPort{}, errors.New("SO_ORIGINAL_DST is only available on Linux")
}
