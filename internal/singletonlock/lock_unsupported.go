//go:build !linux

package singletonlock

import (
	"errors"
	"fmt"
)

var ErrAlreadyRunning = errors.New("platformd is already running")

type Lock struct{}

func Acquire(string, int) (*Lock, error) {
	return nil, fmt.Errorf("daemon locking requires Linux")
}

func (*Lock) Close() error {
	return nil
}
