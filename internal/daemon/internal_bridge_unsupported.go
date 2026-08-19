//go:build !linux

package daemon

import "syscall"

func configureInternalTunnelSocket(_, _ string, _ syscall.RawConn) error {
	return nil
}
