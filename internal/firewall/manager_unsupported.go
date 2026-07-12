//go:build !linux

package firewall

import "fmt"

type Manager struct{}

func New() *Manager {
	return &Manager{}
}

func (*Manager) Apply([]Project) error {
	return fmt.Errorf("platform firewall requires Linux")
}

func (*Manager) Clear() error {
	return fmt.Errorf("platform firewall requires Linux")
}

func (*Manager) Probe() error {
	return fmt.Errorf("platform firewall requires Linux")
}

func EnableIPv4Forwarding() error {
	return fmt.Errorf("IPv4 forwarding configuration requires Linux")
}
