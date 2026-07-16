package domainvariables

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/publicsuffix"
)

var separator = regexp.MustCompile(`_+`)

type Names struct {
	Public   string
	Internal string
}

func OutputNames(hostname string) (Names, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	root, err := publicsuffix.EffectiveTLDPlusOne(hostname)
	if err != nil {
		return Names{}, fmt.Errorf("find domain root for %q: %w", hostname, err)
	}
	prefix := hostname
	if hostname != root {
		prefix = strings.TrimSuffix(hostname, "."+root)
	}
	normalized := normalize(prefix)
	if normalized == "" {
		return Names{}, fmt.Errorf("domain %q has no variable name", hostname)
	}
	return Names{Public: normalized + "_URL", Internal: normalized + "_URL_INTERNAL"}, nil
}

func normalize(value string) string {
	var result strings.Builder
	for _, current := range value {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			result.WriteRune(unicode.ToUpper(current))
		} else {
			result.WriteByte('_')
		}
	}
	normalized := strings.Trim(separator.ReplaceAllString(result.String(), "_"), "_")
	if normalized != "" && normalized[0] >= '0' && normalized[0] <= '9' {
		return "_" + normalized
	}
	return normalized
}
