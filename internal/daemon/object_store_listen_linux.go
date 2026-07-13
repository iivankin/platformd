//go:build linux

package daemon

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

func listenObjectStore(ctx context.Context, address netip.Addr, port uint16) (net.Listener, error) {
	configuration := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(descriptor uintptr) {
			if socketErr = unix.SetsockoptInt(int(descriptor), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); socketErr != nil {
				return
			}
			socketErr = unix.SetsockoptInt(int(descriptor), unix.SOL_IP, unix.IP_FREEBIND, 1)
		}); err != nil {
			return fmt.Errorf("access project S3 socket: %w", err)
		}
		if socketErr != nil {
			return fmt.Errorf("configure project S3 socket: %w", socketErr)
		}
		return nil
	}}
	listener, err := configuration.Listen(ctx, "tcp4", netip.AddrPortFrom(address, port).String())
	if err != nil {
		return nil, fmt.Errorf("listen project S3 endpoint: %w", err)
	}
	return listener, nil
}
