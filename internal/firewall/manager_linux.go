//go:build linux

package firewall

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/google/nftables"
)

const ipv4ForwardPath = "/proc/sys/net/ipv4/ip_forward"

type Manager struct {
	mu sync.Mutex
}

func New() *Manager {
	return &Manager{}
}

func (manager *Manager) Apply(projects []Project) error {
	canonical, err := canonicalProjects(projects)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()

	connection := &nftables.Conn{}
	if err := queueTableDelete(connection, TableName); err != nil {
		return err
	}
	if len(canonical) > 0 {
		compiled := compileRuleset(TableName, canonical)
		compiled.queue(connection)
	}
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("publish platform firewall: %w", err)
	}
	return nil
}

func (manager *Manager) Clear() error {
	return manager.Apply(nil)
}

func (manager *Manager) Probe() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	const probeTable = "platformd-probe"
	connection := &nftables.Conn{}
	if err := queueTableDelete(connection, probeTable); err != nil {
		return err
	}
	connection.AddTable(&nftables.Table{Name: probeTable, Family: nftables.TableFamilyINet})
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("create nf_tables probe: %w", err)
	}
	cleanup := &nftables.Conn{}
	if err := queueTableDelete(cleanup, probeTable); err != nil {
		return err
	}
	if err := cleanup.Flush(); err != nil {
		return fmt.Errorf("remove nf_tables probe: %w", err)
	}
	return nil
}

func EnableIPv4Forwarding() error {
	return enableIPv4ForwardingAt(ipv4ForwardPath)
}

func enableIPv4ForwardingAt(path string) error {
	value, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read IPv4 forwarding state: %w", err)
	}
	if strings.TrimSpace(string(value)) == "1" {
		return nil
	}
	if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	value, err = os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("verify IPv4 forwarding state: %w", err)
	}
	if strings.TrimSpace(string(value)) != "1" {
		return fmt.Errorf("IPv4 forwarding remained %q after write", strings.TrimSpace(string(value)))
	}
	return nil
}

func queueTableDelete(connection *nftables.Conn, name string) error {
	tables, err := connection.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return fmt.Errorf("list inet firewall tables: %w", err)
	}
	for _, table := range tables {
		if table.Name == name {
			connection.DelTable(table)
		}
	}
	return nil
}
