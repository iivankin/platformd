package cloudflaremesh

import (
	"errors"
	"net/netip"
	"strings"
)

var meshIPv4 = netip.MustParsePrefix("100.96.0.0/12")

type NetworkAddress struct {
	InterfaceName string
	Address       string
	NamespacePID  int
}

func parseAddressOutput(output string, namespacePID int) (NetworkAddress, error) {
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		for index, field := range fields {
			if field != "inet" || index+1 >= len(fields) {
				continue
			}
			prefix, err := netip.ParsePrefix(fields[index+1])
			if err == nil && meshIPv4.Contains(prefix.Addr()) {
				return NetworkAddress{
					InterfaceName: "CloudflareWARP", Address: prefix.Addr().String(), NamespacePID: namespacePID,
				}, nil
			}
		}
	}
	return NetworkAddress{}, errors.New("Cloudflare Mesh IPv4 address is not available")
}
