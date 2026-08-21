package hostagent

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

func NormalizeParentURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("parent URL is invalid")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("parent URL must use https, or http with a private IP")
	}
	if parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("parent URL must contain only a scheme and host")
	}
	if parsed.Scheme == "http" {
		address, parseErr := netip.ParseAddr(parsed.Hostname())
		if parseErr != nil || (!address.IsPrivate() && !address.IsLoopback()) {
			return "", errors.New("http parent URL requires a private or loopback IP address")
		}
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func parentHTTPURL(parentURL, path string) (string, error) {
	normalized, err := NormalizeParentURL(parentURL)
	if err != nil {
		return "", err
	}
	return normalized + path, nil
}

func parentWebSocketURL(parentURL, path string) (string, error) {
	normalized, err := NormalizeParentURL(parentURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "http" {
		parsed.Scheme = "ws"
	} else {
		parsed.Scheme = "wss"
	}
	parsed.Path = path
	return parsed.String(), nil
}
