//go:build linux

package rootexec

import "syscall"

func processAttributes(cgroupFD uintptr) (*syscall.SysProcAttr, error) {
	return &syscall.SysProcAttr{
		Setsid: true, Pdeathsig: syscall.SIGKILL,
		UseCgroupFD: true, CgroupFD: int(cgroupFD),
	}, nil
}
