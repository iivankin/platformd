package internaldns

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

func listenDNS(ctx context.Context, address netip.Addr, port uint16, freeBind bool) (net.Listener, net.PacketConn, uint16, error) {
	control, err := socketControl(freeBind)
	if err != nil {
		return nil, nil, 0, err
	}
	configuration := net.ListenConfig{Control: control}
	tcp, err := configuration.Listen(ctx, "tcp4", netip.AddrPortFrom(address, port).String())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("listen internal DNS TCP: %w", err)
	}
	actualPort := uint16(tcp.Addr().(*net.TCPAddr).Port)
	udp, err := configuration.ListenPacket(ctx, "udp4", netip.AddrPortFrom(address, actualPort).String())
	if err != nil {
		_ = tcp.Close()
		return nil, nil, 0, fmt.Errorf("listen internal DNS UDP: %w", err)
	}
	return tcp, udp, actualPort, nil
}
