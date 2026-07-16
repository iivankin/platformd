package variableexpression

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	resourceName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	outputName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Reference struct {
	Resource string
	Output   string
}

// Expand replaces every resource expression in value. Parsing is intentionally
// strict so a mistyped reference fails a deployment instead of reaching the
// application as a deceptively valid-looking literal.
func Expand(value string, resolve func(Reference) (string, error)) (string, error) {
	var result strings.Builder
	remaining := value
	for {
		start := strings.Index(remaining, "${{")
		if start < 0 {
			result.WriteString(remaining)
			return result.String(), nil
		}
		result.WriteString(remaining[:start])
		remaining = remaining[start+3:]
		end := strings.Index(remaining, "}}")
		if end < 0 {
			return "", errors.New("unterminated variable reference")
		}
		reference, err := Parse(strings.TrimSpace(remaining[:end]))
		if err != nil {
			return "", err
		}
		resolved, err := resolve(reference)
		if err != nil {
			return "", err
		}
		result.WriteString(resolved)
		remaining = remaining[end+2:]
	}
}

func Parse(value string) (Reference, error) {
	resource, output, ok := strings.Cut(value, ".")
	if !ok || strings.Contains(output, ".") || !resourceName.MatchString(resource) || !outputName.MatchString(output) {
		return Reference{}, fmt.Errorf("invalid variable reference %q", value)
	}
	return Reference{Resource: resource, Output: output}, nil
}

func References(value string) ([]Reference, error) {
	references := make([]Reference, 0)
	_, err := Expand(value, func(reference Reference) (string, error) {
		references = append(references, reference)
		return "", nil
	})
	return references, err
}
