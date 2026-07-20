//go:build !linux

package portproxy

import (
	"errors"
	"net"
)

var errNetworkNamespaceUnsupported = errors.New("network namespace sockets are available only on Linux")

func listenTCPInNamespace(pid int, address string) (net.Listener, error) {
	if pid != 0 {
		return nil, errNetworkNamespaceUnsupported
	}
	return net.Listen("tcp4", address)
}

func listenUDPInNamespace(pid int, address *net.UDPAddr) (*net.UDPConn, error) {
	if pid != 0 {
		return nil, errNetworkNamespaceUnsupported
	}
	return net.ListenUDP("udp4", address)
}

func dialTCPInNamespace(pid int, dialer net.Dialer, address string) (net.Conn, error) {
	if pid != 0 {
		return nil, errNetworkNamespaceUnsupported
	}
	return dialer.Dial("tcp4", address)
}

func dialUDPInNamespace(pid int, source, target *net.UDPAddr) (*net.UDPConn, error) {
	if pid != 0 {
		return nil, errNetworkNamespaceUnsupported
	}
	return net.DialUDP("udp4", source, target)
}
