//go:build !linux

package internaldns

import (
	"fmt"
	"syscall"
)

func socketControl(freeBind bool) (func(string, string, syscall.RawConn) error, error) {
	if freeBind {
		return nil, fmt.Errorf("IP_FREEBIND requires Linux")
	}
	return nil, nil
}
