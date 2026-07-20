//go:build !linux || !amd64 || !cgo

package containerengine

import (
	"context"
	"io"
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

func (*Engine) ContainerNetworkCounters(string) (NetworkCounters, error) {
	return NetworkCounters{}, ErrUnsupported
}

func (*Engine) ContainerListeningPorts(string) ([]ListeningPort, error) {
	return nil, ErrUnsupported
}

func (*Engine) ExecContainer(context.Context, string, ExecRequest) (int, error) {
	return -1, ErrUnsupported
}

func (*Engine) ExecTerminalContainer(context.Context, string, TerminalExecRequest) (int, error) {
	return -1, ErrUnsupported
}

func (*Engine) ListContainerFiles(context.Context, string, string) ([]ContainerFileEntry, error) {
	return nil, ErrUnsupported
}

func (*Engine) OpenContainerFile(context.Context, string, string) (io.ReadCloser, ContainerFileEntry, error) {
	return nil, ContainerFileEntry{}, ErrUnsupported
}

func (*Engine) WriteContainerFile(context.Context, string, string, io.Reader, int64) error {
	return ErrUnsupported
}
