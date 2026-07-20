package containerengine

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type procSocketTable struct {
	protocol string
	state    string
}

func parseProcListeningPorts(reader io.Reader, table procSocketTable) ([]ListeningPort, error) {
	if table.protocol != "tcp" && table.protocol != "udp" {
		return nil, errors.New("socket table protocol must be tcp or udp")
	}
	if table.state == "" {
		return nil, errors.New("socket table listening state is required")
	}

	ports := make([]ListeningPort, 0)
	scanner := bufio.NewScanner(reader)
	line := 0
	for scanner.Scan() {
		line++
		if line == 1 {
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || !strings.EqualFold(fields[3], table.state) {
			continue
		}
		addressAndPort := strings.Split(fields[1], ":")
		if len(addressAndPort) != 2 {
			return nil, fmt.Errorf("parse socket table line %d: invalid local address", line)
		}
		address, err := parseProcNetworkAddress(addressAndPort[0])
		if err != nil {
			return nil, fmt.Errorf("parse socket table line %d: %w", line, err)
		}
		// Ingress and public listeners connect to the container IP. A service
		// bound only to loopback is not reachable through either route.
		if address.IsLoopback() {
			continue
		}
		value, err := strconv.ParseUint(addressAndPort[1], 16, 16)
		if err != nil || value == 0 {
			return nil, fmt.Errorf("parse socket table line %d: invalid local port", line)
		}
		ports = append(ports, ListeningPort{Port: int(value), Protocol: table.protocol})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan socket table: %w", err)
	}
	return ports, nil
}

func parseProcNetworkAddress(encoded string) (netip.Addr, error) {
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return netip.Addr{}, errors.New("invalid local address")
	}
	switch len(raw) {
	case 4:
		reverseBytes(raw)
	case 16:
		// Linux exposes IPv6 addresses as four little-endian uint32 words.
		for offset := 0; offset < len(raw); offset += 4 {
			reverseBytes(raw[offset : offset+4])
		}
	default:
		return netip.Addr{}, errors.New("invalid local address size")
	}
	address, ok := netip.AddrFromSlice(raw)
	if !ok {
		return netip.Addr{}, errors.New("invalid local address")
	}
	return address.Unmap(), nil
}

func reverseBytes(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}

func uniqueListeningPorts(ports []ListeningPort) []ListeningPort {
	unique := make(map[ListeningPort]struct{}, len(ports))
	for _, port := range ports {
		unique[port] = struct{}{}
	}
	result := make([]ListeningPort, 0, len(unique))
	for port := range unique {
		result = append(result, port)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Port == result[right].Port {
			return result[left].Protocol < result[right].Protocol
		}
		return result[left].Port < result[right].Port
	})
	return result
}
