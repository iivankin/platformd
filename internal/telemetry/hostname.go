package telemetry

import (
	"errors"
	"net"
	"strings"

	"github.com/iivankin/platformd/internal/publichostname"
)

func requestHostname(hostport string) (string, error) {
	value := strings.TrimSpace(hostport)
	if value == "" {
		return "", errors.New("request hostname is empty")
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return publichostname.Normalize(value)
}
