package resourcename

import (
	"errors"
	"regexp"
)

var label = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func Validate(value string) error {
	if !label.MatchString(value) {
		return errors.New("name must be a lowercase DNS label containing 1..63 letters, digits, or hyphens")
	}
	return nil
}
