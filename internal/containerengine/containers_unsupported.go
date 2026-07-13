//go:build !linux || !amd64 || !cgo

package containerengine

import (
	"context"
	"syscall"
)

func (*Engine) CreateContainer(context.Context, ContainerSpec) (Container, error) {
	return Container{}, ErrUnsupported
}

func (*Engine) StartContainer(context.Context, string) error {
	return ErrUnsupported
}

func (*Engine) StopContainer(string, uint) error {
	return ErrUnsupported
}

func (*Engine) KillContainer(string, syscall.Signal) error {
	return ErrUnsupported
}

func (*Engine) WaitContainer(context.Context, string) (int32, error) {
	return -1, ErrUnsupported
}

func (*Engine) RemoveContainer(context.Context, string, bool) error {
	return ErrUnsupported
}

func (*Engine) InspectContainer(string) (Container, error) {
	return Container{}, ErrUnsupported
}

func (*Engine) ExecContainer(context.Context, string, ExecRequest) (int, error) {
	return -1, ErrUnsupported
}

func (*Engine) ExecTerminalContainer(context.Context, string, TerminalExecRequest) (int, error) {
	return -1, ErrUnsupported
}
