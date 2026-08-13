//go:build linux

package telemetry

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func listenServiceGateway(address netip.Addr, port uint16) (net.Listener, error) {
	configuration := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(descriptor uintptr) {
			socketErr = unix.SetsockoptInt(int(descriptor), unix.SOL_IP, unix.IP_FREEBIND, 1)
		}); err != nil {
			return fmt.Errorf("access service telemetry socket: %w", err)
		}
		if socketErr != nil {
			return fmt.Errorf("configure service telemetry socket: %w", socketErr)
		}
		return nil
	}}
	return configuration.Listen(
		context.Background(), "tcp4", net.JoinHostPort(address.String(), strconv.Itoa(int(port))),
	)
}
