package id

import (
	"fmt"

	"github.com/nrednav/cuid2"
)

const Length = 24

func New() (value string, err error) {
	// The upstream generator panics when OS entropy is unavailable. Preserve
	// platformd's explicit ID allocation error contract at that boundary.
	defer func() {
		if recovered := recover(); recovered != nil {
			value = ""
			err = fmt.Errorf("generate CUID2: %v", recovered)
		}
	}()
	value = cuid2.Generate()
	if !Valid(value) {
		return "", fmt.Errorf("generate CUID2: invalid value %q", value)
	}
	return value, nil
}

func Valid(value string) bool {
	return len(value) == Length && cuid2.IsCuid(value)
}
