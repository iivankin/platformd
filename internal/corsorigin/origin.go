package corsorigin

import (
	"errors"
	"net/url"
	"strings"
)

func NormalizeAll(origins []string) ([]string, error) {
	if len(origins) > 32 {
		return nil, errors.New("CORS allowlist cannot exceed 32 origins")
	}
	result := make([]string, 0, len(origins))
	seen := make(map[string]struct{}, len(origins))
	for _, value := range origins {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
			return nil, errors.New("CORS origins must be exact HTTP or HTTPS origins")
		}
		normalized := strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}
