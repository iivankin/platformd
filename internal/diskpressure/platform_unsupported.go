//go:build !linux

package diskpressure

import "errors"

var errUnsupported = errors.New("disk pressure requires Linux")

type StatfsCollector struct{}

func (StatfsCollector) Collect(string) (Usage, error) {
	return Usage{}, errUnsupported
}

type FileReserve struct{}

func NewFileReserve(int) (*FileReserve, error) {
	return nil, errUnsupported
}

func (*FileReserve) Present(string, int64) (bool, error) {
	return false, errUnsupported
}

func (*FileReserve) Ensure(string, int64) error {
	return errUnsupported
}

func (*FileReserve) Remove(string) error {
	return errUnsupported
}
