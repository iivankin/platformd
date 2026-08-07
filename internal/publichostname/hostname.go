package publichostname

import (
	"errors"
	"net"
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

func Normalize(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasSuffix(trimmed, ".") || strings.ContainsAny(trimmed, "/\\@?# ") {
		return "", errors.New("hostname must be a public DNS name without scheme, port, path, or trailing dot")
	}
	ascii, err := idna.Lookup.ToASCII(strings.ToLower(trimmed))
	if err != nil {
		return "", errors.New("hostname is not valid IDNA")
	}
	if len(ascii) > 253 || !strings.ContainsRune(ascii, '.') {
		return "", errors.New("hostname must be a multi-label DNS name at most 253 bytes")
	}
	if _, err := netip.ParseAddr(ascii); err == nil {
		return "", errors.New("IP address is not a public hostname")
	}
	for _, label := range strings.Split(ascii, ".") {
		if !validLabel(label) {
			return "", errors.New("hostname contains an invalid DNS label")
		}
	}
	return ascii, nil
}

func NormalizeHostHeader(value string) (string, error) {
	host := value
	if parsed, port, err := net.SplitHostPort(value); err == nil {
		if port != "443" {
			return "", errors.New("HTTPS Host header contains an unexpected port")
		}
		host = parsed
	}
	return Normalize(host)
}

// IsApex reports whether hostname is a registrable domain (eTLD+1), e.g.
// example.com but not app.example.com.
func IsApex(hostname string) (bool, error) {
	normalized, err := Normalize(hostname)
	if err != nil {
		return false, err
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(normalized)
	if err != nil {
		return false, err
	}
	return normalized == root, nil
}

// NormalizeApex normalizes a hostname and requires it to be an apex domain.
func NormalizeApex(value string) (string, error) {
	normalized, err := Normalize(value)
	if err != nil {
		return "", err
	}
	ok, err := IsApex(normalized)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("preview domain must be a root domain covered by an Origin certificate")
	}
	return normalized, nil
}

func validLabel(label string) bool {
	if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, character := range label {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return false
	}
	return true
}
