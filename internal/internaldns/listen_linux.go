//go:build linux

package internaldns

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

func socketControl(freeBind bool) (func(string, string, syscall.RawConn) error, error) {
	return func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(descriptor uintptr) {
			if socketErr = unix.SetsockoptInt(int(descriptor), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); socketErr != nil {
				return
			}
			if freeBind {
				socketErr = unix.SetsockoptInt(int(descriptor), unix.SOL_IP, unix.IP_FREEBIND, 1)
			}
		}); err != nil {
			return fmt.Errorf("access internal DNS socket: %w", err)
		}
		if socketErr != nil {
			return fmt.Errorf("configure internal DNS socket: %w", socketErr)
		}
		return nil
	}, nil
}
