//go:build !linux

package rootexec

import (
	"errors"
	"syscall"
)

func processAttributes(uintptr) (*syscall.SysProcAttr, error) {
	return nil, errors.New("server exec requires Linux cgroup v2")
}
