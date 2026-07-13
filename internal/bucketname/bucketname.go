package bucketname

import (
	"errors"
	"net"
	"strings"
)

func Validate(value string) error {
	if len(value) < 3 || len(value) > 63 {
		return errors.New("bucket name must be 3..63 characters")
	}
	if net.ParseIP(value) != nil || strings.Contains(value, "..") || strings.Contains(value, ".-") || strings.Contains(value, "-.") {
		return errors.New("bucket name must be a DNS-style name, not an IP address")
	}
	for index, character := range value {
		letterOrDigit := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if !letterOrDigit && character != '-' && character != '.' {
			return errors.New("bucket name may contain only lowercase letters, digits, dots, and hyphens")
		}
		if (index == 0 || index == len(value)-1) && !letterOrDigit {
			return errors.New("bucket name must start and end with a letter or digit")
		}
	}
	return nil
}
