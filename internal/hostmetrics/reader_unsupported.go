//go:build !linux

package hostmetrics

import "errors"

func NewProduction() (Reader, error) {
	return nil, errors.New("host metrics require Linux")
}
