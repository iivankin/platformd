//go:build linux

package daemon

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func configureInternalTunnelSocket(_, _ string, conn syscall.RawConn) error {
	var sockErr error
	if err := conn.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_TRANSPARENT, 1)
	}); err != nil {
		return err
	}
	return sockErr
}
