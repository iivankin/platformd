package internaldns

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

func ReadUpstreams(path string) ([]netip.AddrPort, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open resolver configuration: %w", err)
	}
	defer file.Close()
	var result []netip.AddrPort
	seen := make(map[netip.Addr]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		address, err := netip.ParseAddr(strings.Trim(fields[1], "[]"))
		if err != nil || !address.Is4() {
			continue
		}
		address = address.Unmap()
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, netip.AddrPortFrom(address, 53))
		if len(result) == 3 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read resolver configuration: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("resolver configuration %s has no IPv4 nameserver", path)
	}
	return result, nil
}
