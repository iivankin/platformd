package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

type objectStoreDetails interface {
	Details(context.Context, string, string) (objectstore.StoreDetails, error)
	Stores(context.Context, string) ([]state.ObjectStore, error)
}

type objectStoreDataPlane interface {
	ReconcileBuckets(context.Context, []string) error
	ConfigureDataPlaneProject(context.Context, string, string, []objectstore.DataPlaneStore) error
	RemoveDataPlaneProject(context.Context, string) error
	BeginDataPlaneQuiesce(context.Context) error
	EndDataPlaneQuiesce(context.Context) error
}

func (stack *runtimeStack) ConfigureObjectStores(ctx context.Context, store *state.Store, details objectStoreDetails, dataPlane objectStoreDataPlane) error {
	if store == nil || details == nil || dataPlane == nil {
		return errors.New("object store runtime dependencies are incomplete")
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	stack.objectStoreDetails = details
	stack.objectStoreDataPlane = dataPlane
	stack.mu.Unlock()

	stores, err := store.ObjectStores(ctx)
	if err != nil {
		return err
	}
	storeIDs := make([]string, len(stores))
	for index := range stores {
		storeIDs[index] = stores[index].ID
	}
	if err := dataPlane.ReconcileBuckets(ctx, storeIDs); err != nil {
		return fmt.Errorf("reconcile RustFS buckets with SQLite: %w", err)
	}
	configuredProjects := make(map[string]bool)
	for _, objectStore := range stores {
		if !configuredProjects[objectStore.ProjectID] {
			if err := stack.configureObjectStoreProject(ctx, objectStore.ProjectID); err != nil {
				stack.recordObjectStoreFailure(objectStore.ProjectID, err)
				continue
			}
			configuredProjects[objectStore.ProjectID] = true
		}
		if err := stack.publishObjectStore(objectStore); err != nil {
			stack.recordObjectStoreFailure(objectStore.ProjectID, err)
		}
	}
	return nil
}

// EnableObjectStore publishes a project-local S3 endpoint after desired state
// is committed. Repeating it refreshes the whole project snapshot in Rust and
// repairs DNS/firewall state without adding another durable state machine.
func (stack *runtimeStack) EnableObjectStore(ctx context.Context, objectStore state.ObjectStore) error {
	if err := stack.configureObjectStoreProject(ctx, objectStore.ProjectID); err != nil {
		return err
	}
	return stack.publishObjectStore(objectStore)
}

func (stack *runtimeStack) configureObjectStoreProject(ctx context.Context, projectID string) error {
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	details := stack.objectStoreDetails
	dataPlane := stack.objectStoreDataPlane
	network, exists := stack.projectNetworks[projectID]
	stack.mu.Unlock()
	if details == nil || dataPlane == nil {
		return errors.New("object store runtime is not configured")
	}
	if !exists {
		return fmt.Errorf("project %s network runtime is unavailable", projectID)
	}
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		return fmt.Errorf("parse project gateway: %w", err)
	}
	stores, err := details.Stores(ctx, projectID)
	if err != nil {
		return err
	}
	configured := make([]objectstore.DataPlaneStore, 0, len(stores))
	for _, stored := range stores {
		entry, err := details.Details(ctx, projectID, stored.ID)
		if err != nil {
			return fmt.Errorf("load S3 data-plane credential: %w", err)
		}
		configured = append(configured, objectstore.DataPlaneStore{
			StoreID: entry.Store.ID, BucketName: entry.Store.BucketName,
			AccessKey: entry.AccessKey, Secret: entry.Secret,
			Permission: entry.Credential.Permission, CORSOrigins: entry.Store.CORSOrigins,
		})
	}
	address := net.JoinHostPort(gateway.String(), strconv.Itoa(firewall.ObjectStorePort))
	if err := dataPlane.ConfigureDataPlaneProject(ctx, projectID, address, configured); err != nil {
		return fmt.Errorf("configure Rust S3 endpoint: %w", err)
	}
	return nil
}

func (stack *runtimeStack) publishObjectStore(objectStore state.ObjectStore) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return errors.New("container runtime is closed")
	}
	network, networkExists := stack.projectNetworks[objectStore.ProjectID]
	zone := stack.dnsZones[objectStore.ProjectID]
	project, projectExists := stack.firewallProjects[objectStore.ProjectID]
	if !networkExists || zone == nil || !projectExists {
		return fmt.Errorf("project %s network runtime is unavailable", objectStore.ProjectID)
	}
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		return fmt.Errorf("parse project gateway: %w", err)
	}

	hostname := objectStore.Name + "." + objectStore.ProjectName + ".internal"
	if err := zone.Set(hostname, gateway); err != nil {
		return err
	}
	if !project.ObjectStoreEnabled {
		project.ObjectStoreEnabled = true
		candidate := make([]firewall.Project, 0, len(stack.firewallProjects))
		for projectID, current := range stack.firewallProjects {
			if projectID == objectStore.ProjectID {
				current = project
			}
			candidate = append(candidate, current)
		}
		if err := stack.firewall.Apply(candidate); err != nil {
			_ = zone.Delete(hostname)
			return err
		}
		stack.firewallProjects[objectStore.ProjectID] = project
	}
	stack.objectStoreProjects[objectStore.ProjectID] = true
	delete(stack.objectStoreFailures, objectStore.ProjectID)
	return nil
}

func (stack *runtimeStack) ObjectStoreStatus(projectID string) (string, string) {
	stack.mu.Lock()
	running := stack.objectStoreProjects[projectID]
	failure := stack.objectStoreFailures[projectID]
	closed := stack.closed
	stack.mu.Unlock()
	if failure != nil {
		return "failed", failure.Error()
	}
	if closed || !running {
		return "pending", "S3 endpoint is not ready"
	}
	return "running", ""
}

func (stack *runtimeStack) recordObjectStoreFailure(projectID string, err error) {
	stack.mu.Lock()
	stack.objectStoreFailures[projectID] = err
	stack.mu.Unlock()
}
