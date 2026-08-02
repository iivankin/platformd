//go:build linux

package portproxy

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// TCP_ESTABLISHED is 1 in Linux's stable TCP_INFO userspace ABI.
const tcpInfoStateEstablished = 1

func readTCPSocketCounters(connection net.Conn) (uint64, uint64, bool) {
	socket, ok := connection.(syscall.Conn)
	if !ok {
		return 0, 0, false
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return 0, 0, false
	}
	var info *unix.TCPInfo
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		info, socketErr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
	}); err != nil || socketErr != nil || info == nil {
		return 0, 0, false
	}
	// Linux accounts FIN as a sequence byte in these fields. Once either side
	// half-closes, let the final io.Copy payload counts perform reconciliation.
	if info.State != tcpInfoStateEstablished {
		return 0, 0, false
	}
	return info.Bytes_received, info.Bytes_acked, true
}
