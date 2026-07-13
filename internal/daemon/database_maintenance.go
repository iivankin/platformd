package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/iivankin/platformd/internal/firewall"
)

// BlockDatabase publishes an ephemeral forward drop in platformd's single
// nftables ruleset. The returned release function removes exactly that drop;
// neither side creates durable state.
func (stack *runtimeStack) BlockDatabase(
	ctx context.Context,
	projectID string,
	address netip.Addr,
	port uint16,
) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint := firewall.DatabaseEndpoint{Address: address, Port: port}
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return nil, errors.New("container runtime is closed")
	}
	project, exists := stack.firewallProjects[projectID]
	if !exists {
		return nil, fmt.Errorf("project %s firewall runtime is unavailable", projectID)
	}
	if slices.Contains(project.BlockedDatabaseEndpoints, endpoint) {
		return nil, fmt.Errorf("database endpoint %s:%d is already in maintenance", address, port)
	}
	project.BlockedDatabaseEndpoints = append(slices.Clone(project.BlockedDatabaseEndpoints), endpoint)
	if err := stack.applyFirewallProjectLocked(project); err != nil {
		return nil, err
	}
	stack.firewallProjects[projectID] = project

	release := func() error {
		stack.mu.Lock()
		defer stack.mu.Unlock()
		if stack.closed {
			return nil
		}
		current, exists := stack.firewallProjects[projectID]
		if !exists {
			return nil
		}
		index := slices.Index(current.BlockedDatabaseEndpoints, endpoint)
		if index < 0 {
			return nil
		}
		current.BlockedDatabaseEndpoints = slices.Delete(
			slices.Clone(current.BlockedDatabaseEndpoints), index, index+1,
		)
		if err := stack.applyFirewallProjectLocked(current); err != nil {
			return err
		}
		stack.firewallProjects[projectID] = current
		return nil
	}
	return release, nil
}

func (stack *runtimeStack) applyFirewallProjectLocked(replacement firewall.Project) error {
	projects := make([]firewall.Project, 0, len(stack.firewallProjects))
	for projectID, current := range stack.firewallProjects {
		if projectID == replacement.ID {
			current = replacement
		}
		projects = append(projects, current)
	}
	return stack.firewall.Apply(projects)
}
