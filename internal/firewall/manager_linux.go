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
	mu           sync.Mutex
	projects     []Project
	trafficCarry map[string]PublicTrafficCounters
}

func New() *Manager {
	return &Manager{trafficCarry: make(map[string]PublicTrafficCounters)}
}

func (manager *Manager) Apply(projects []Project) error {
	canonical, err := canonicalProjects(projects)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()

	connection := &nftables.Conn{}
	preservedTraffic := clonePublicTraffic(manager.trafficCarry)
	tableExists, err := queueTableDelete(connection, TableName)
	if err != nil {
		return err
	}
	if tableExists && len(manager.projects) > 0 {
		currentTraffic, readErr := readPublicTraffic(connection, manager.projects)
		if readErr != nil {
			return fmt.Errorf("preserve public traffic counters: %w", readErr)
		}
		for serviceID, counters := range currentTraffic {
			preservedTraffic[serviceID] = counters
		}
	}
	if len(canonical) > 0 {
		compiled := compileRuleset(TableName, canonical)
		// Recreated counter objects start from the old cumulative value, so an
		// unrelated firewall mutation cannot look like a traffic counter reset.
		seedPublicTrafficCounters(compiled.objects, preservedTraffic)
		compiled.queue(connection)
	}
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("publish platform firewall: %w", err)
	}
	for _, project := range canonical {
		for _, endpoint := range project.PublicTrafficEndpoints {
			delete(preservedTraffic, endpoint.ServiceID)
		}
	}
	manager.trafficCarry = preservedTraffic
	manager.projects = canonical
	return nil
}

// PublicTraffic reads every named counter in one netlink dump and maps the
// process-owned object names back to services.
func (manager *Manager) PublicTraffic() (map[string]PublicTrafficCounters, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	result := make(map[string]PublicTrafficCounters)
	if len(manager.projects) == 0 {
		return result, nil
	}
	current, err := readPublicTraffic(&nftables.Conn{}, manager.projects)
	if err != nil {
		return nil, err
	}
	for _, project := range manager.projects {
		for _, endpoint := range project.PublicTrafficEndpoints {
			result[endpoint.ServiceID] = current[endpoint.ServiceID]
		}
	}
	return result, nil
}

func readPublicTraffic(connection *nftables.Conn, projects []Project) (map[string]PublicTrafficCounters, error) {
	result := make(map[string]PublicTrafficCounters)
	if len(projects) == 0 {
		return result, nil
	}
	objects, err := connection.GetObjects(&nftables.Table{Name: TableName, Family: nftables.TableFamilyINet})
	if err != nil {
		return nil, fmt.Errorf("read public traffic counters: %w", err)
	}
	values := make(map[string]uint64, len(objects))
	for _, object := range objects {
		if counter, ok := object.(*nftables.CounterObj); ok {
			values[counter.Name] = counter.Bytes
		}
	}
	for _, project := range projects {
		for _, endpoint := range project.PublicTrafficEndpoints {
			result[endpoint.ServiceID] = PublicTrafficCounters{
				IngressBytes: values[publicCounterName("ingress", endpoint.ServiceID)],
				EgressBytes:  values[publicCounterName("egress", endpoint.ServiceID)],
			}
		}
	}
	return result, nil
}

func clonePublicTraffic(current map[string]PublicTrafficCounters) map[string]PublicTrafficCounters {
	result := make(map[string]PublicTrafficCounters, len(current))
	for serviceID, counters := range current {
		result[serviceID] = counters
	}
	return result
}

func seedPublicTrafficCounters(objects []nftables.Obj, counters map[string]PublicTrafficCounters) {
	values := make(map[string]uint64, len(counters)*2)
	for serviceID, current := range counters {
		values[publicCounterName("ingress", serviceID)] = current.IngressBytes
		values[publicCounterName("egress", serviceID)] = current.EgressBytes
	}
	for _, object := range objects {
		if counter, ok := object.(*nftables.CounterObj); ok {
			counter.Bytes = values[counter.Name]
		}
	}
}

func (manager *Manager) Clear() error {
	return manager.Apply(nil)
}

func (manager *Manager) Probe() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	const probeTable = "platformd-probe"
	connection := &nftables.Conn{}
	if _, err := queueTableDelete(connection, probeTable); err != nil {
		return err
	}
	connection.AddTable(&nftables.Table{Name: probeTable, Family: nftables.TableFamilyINet})
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("create nf_tables probe: %w", err)
	}
	cleanup := &nftables.Conn{}
	if _, err := queueTableDelete(cleanup, probeTable); err != nil {
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

func queueTableDelete(connection *nftables.Conn, name string) (bool, error) {
	tables, err := connection.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return false, fmt.Errorf("list inet firewall tables: %w", err)
	}
	for _, table := range tables {
		if table.Name == name {
			connection.DelTable(table)
			return true, nil
		}
	}
	return false, nil
}
