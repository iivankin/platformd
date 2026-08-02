//go:build !linux

package portproxy

import "net"

func readTCPSocketCounters(net.Conn) (uint64, uint64, bool) {
	return 0, 0, false
}
