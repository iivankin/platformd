package internaldns

import (
	"fmt"
	"net/netip"
	"strings"
	"sync"

	"golang.org/x/net/dns/dnsmessage"
)

const internalSuffix = ".internal."

type Zone struct {
	mu      sync.RWMutex
	records map[string][4]byte
}

func NewZone(records map[string]netip.Addr) (*Zone, error) {
	zone := &Zone{}
	if err := zone.Replace(records); err != nil {
		return nil, err
	}
	return zone, nil
}

func (zone *Zone) Replace(records map[string]netip.Addr) error {
	canonical := make(map[string][4]byte, len(records))
	for name, address := range records {
		fqdn, err := canonicalInternalName(name)
		if err != nil {
			return err
		}
		if !address.IsValid() || !address.Is4() {
			return fmt.Errorf("internal DNS record %s requires an IPv4 address", name)
		}
		canonical[fqdn] = address.As4()
	}
	zone.mu.Lock()
	zone.records = canonical
	zone.mu.Unlock()
	return nil
}

func (zone *Zone) lookup(name string) ([4]byte, bool) {
	zone.mu.RLock()
	defer zone.mu.RUnlock()
	address, found := zone.records[strings.ToLower(name)]
	return address, found
}

func canonicalInternalName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !strings.HasSuffix(value, ".") {
		value += "."
	}
	if !strings.HasSuffix(value, internalSuffix) || value == "internal." {
		return "", fmt.Errorf("DNS name %q is outside .internal", value)
	}
	name, err := dnsmessage.NewName(value)
	if err != nil {
		return "", fmt.Errorf("invalid internal DNS name %q: %w", value, err)
	}
	return strings.ToLower(name.String()), nil
}
