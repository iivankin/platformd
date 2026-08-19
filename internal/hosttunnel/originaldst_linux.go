//go:build linux

package hosttunnel

import (
	"fmt"
	"net"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/unix"
)

const soOriginalDst = 80

func OriginalDestination(conn *net.TCPConn) (netip.AddrPort, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return netip.AddrPort{}, err
	}
	var addr unix.RawSockaddrInet4
	var errno unix.Errno
	if controlErr := raw.Control(func(fd uintptr) {
		size := uint32(unsafe.Sizeof(addr))
		_, _, errno = unix.Syscall6(
			unix.SYS_GETSOCKOPT, fd, unix.IPPROTO_IP, soOriginalDst,
			uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&size)), 0,
		)
	}); controlErr != nil {
		return netip.AddrPort{}, controlErr
	}
	if errno != 0 {
		return netip.AddrPort{}, fmt.Errorf("SO_ORIGINAL_DST: %w", errno)
	}
	ip := netip.AddrFrom4(addr.Addr)
	port := uint16(addr.Port>>8) | uint16(addr.Port<<8)
	return netip.AddrPortFrom(ip, port), nil
}
