//go:build !linux

package telemetry

import (
	"net"
	"net/netip"
	"strconv"
)

func listenServiceGateway(address netip.Addr, port uint16) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort(address.String(), strconv.Itoa(int(port))))
}
