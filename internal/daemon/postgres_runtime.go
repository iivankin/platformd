package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"path"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func (stack *runtimeStack) ConfigureManagedPostgres(ctx context.Context, store *state.Store, master cryptobox.MasterKey) error {
	controller, err := managedpostgres.NewController(managedpostgres.ControllerConfig{
		Store: store, Engine: stack.engine, Publisher: stack, Growth: stack.growth, Admission: stack.admission,
		OwnerPassword: func(resource state.ManagedPostgres) (string, error) {
			return managedpostgres.OpenOwnerPassword(master, resource.ID, resource.OwnerPasswordEncrypted)
		},
		BootstrapPassword: func(resource state.ManagedPostgres) (string, error) {
			return managedpostgres.OpenBootstrapPassword(master, resource.ID, resource.BootstrapPasswordEncrypted)
		},
		Placement: stack.postgresPlacement, VolumeRoot: stack.paths.VolumesRoot,
		LogRoot: stack.paths.LogsRoot, LogSizeBytes: serviceLogSegmentBytes,
		LogMaxFiles: serviceLogMaxFiles,
	})
	if err != nil {
		return err
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	stack.managedPostgres = controller
	stack.mu.Unlock()
	resources, err := store.ManagedPostgresResources(ctx)
	if err != nil {
		return err
	}
	for _, resource := range resources {
		if err := controller.Start(ctx, resource.ID); err != nil {
			stack.recordPostgresFailure(resource.ID, err)
		}
	}
	return nil
}

func (stack *runtimeStack) OpenManagedPostgresBackup(ctx context.Context, resourceID string) (io.ReadCloser, error) {
	stack.mu.Lock()
	controller := stack.managedPostgres
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return nil, errors.New("managed PostgreSQL runtime is not ready")
	}
	return controller.OpenBackupDump(ctx, resourceID)
}

func (stack *runtimeStack) RestoreManagedPostgres(
	ctx context.Context,
	resourceID string,
	dump io.Reader,
	actor managedpostgres.Actor,
) error {
	stack.mu.Lock()
	controller := stack.managedPostgres
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("managed PostgreSQL runtime is not ready")
	}
	err := controller.RestoreReplace(ctx, resourceID, dump, actor)
	stack.mu.Lock()
	if err == nil {
		delete(stack.postgresFailures, resourceID)
	} else {
		stack.postgresFailures[resourceID] = err
	}
	stack.mu.Unlock()
	return err
}

func (stack *runtimeStack) postgresPlacement(resource state.ManagedPostgres) (managedpostgres.Placement, error) {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return managedpostgres.Placement{}, errors.New("container runtime is closed")
	}
	network, ok := stack.projectNetworks[resource.ProjectID]
	if !ok {
		return managedpostgres.Placement{}, fmt.Errorf("project %s has no runtime network", resource.ProjectID)
	}
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		return managedpostgres.Placement{}, fmt.Errorf("parse project gateway: %w", err)
	}
	return managedpostgres.Placement{
		NetworkName: network.Name, Gateway: gateway, DNSSearch: resource.ProjectName + ".internal",
		CgroupParent: path.Join(stack.cgroupRoot, "postgres-"+resource.ID),
	}, nil
}

func (stack *runtimeStack) PublishPostgres(resource state.ManagedPostgres, container containerengine.Container) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	zone := stack.dnsZones[resource.ProjectID]
	network, ok := stack.projectNetworks[resource.ProjectID]
	if zone == nil || !ok {
		return fmt.Errorf("project %s DNS runtime is unavailable", resource.ProjectID)
	}
	addresses := container.IPs[network.Name]
	if len(addresses) != 1 {
		return fmt.Errorf("managed PostgreSQL container has %d project addresses, want one", len(addresses))
	}
	address, err := netip.ParseAddr(addresses[0])
	if err != nil {
		return err
	}
	if err := zone.Set(resource.Name+"."+resource.ProjectName+".internal", address); err != nil {
		return err
	}
	delete(stack.postgresFailures, resource.ID)
	return nil
}

func (stack *runtimeStack) WithdrawPostgres(resource state.ManagedPostgres) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	zone := stack.dnsZones[resource.ProjectID]
	if zone == nil {
		return fmt.Errorf("project %s DNS runtime is unavailable", resource.ProjectID)
	}
	return zone.Delete(resource.Name + "." + resource.ProjectName + ".internal")
}

func (stack *runtimeStack) PostgresStatus(resourceID string) (string, string) {
	stack.mu.Lock()
	controller := stack.managedPostgres
	failure := stack.postgresFailures[resourceID]
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		if failure != nil {
			return "failed", failure.Error()
		}
		return "pending", "Runtime is not ready"
	}
	container, active, err := controller.Status(resourceID)
	if err != nil {
		return "failed", err.Error()
	}
	if active && container.State == "running" {
		return "running", ""
	}
	if failure != nil {
		return "failed", failure.Error()
	}
	return "pending", "Waiting for the first successful start"
}

func (stack *runtimeStack) ResolveManagedPostgresImage(ctx context.Context, tag string) (string, error) {
	reference, err := managedimages.Reference(managedimages.PostgreSQL, tag)
	if err != nil {
		return "", err
	}
	stack.mu.Lock()
	closed := stack.closed
	engine := stack.engine
	stack.mu.Unlock()
	if closed {
		return "", errors.New("container runtime is closed")
	}
	if err := stack.growth.PermitGrowth(ctx); err != nil {
		return "", err
	}
	image, err := engine.Pull(ctx, containerengine.PullRequest{Reference: reference, Refresh: true})
	if err != nil {
		return "", err
	}
	if image.ID == "" || image.Digest == "" {
		return "", errors.New("resolved managed PostgreSQL image has no ID or digest")
	}
	if _, err := serviceconfig.PinnedReference(reference, image.Digest); err != nil {
		return "", err
	}
	return image.Digest, nil
}

func (stack *runtimeStack) StartManagedPostgres(ctx context.Context, resourceID string) error {
	stack.mu.Lock()
	controller := stack.managedPostgres
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("managed PostgreSQL runtime is not ready")
	}
	err := controller.Start(ctx, resourceID)
	stack.mu.Lock()
	if err == nil {
		delete(stack.postgresFailures, resourceID)
	} else {
		stack.postgresFailures[resourceID] = err
	}
	stack.mu.Unlock()
	return err
}

func (stack *runtimeStack) QueryManagedPostgres(ctx context.Context, resourceID, sql string) (managedpostgres.QueryResult, error) {
	stack.mu.Lock()
	controller := stack.managedPostgres
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return managedpostgres.QueryResult{}, errors.New("managed PostgreSQL runtime is not ready")
	}
	return controller.Query(ctx, resourceID, sql)
}

func (stack *runtimeStack) recordPostgresFailure(resourceID string, err error) {
	stack.mu.Lock()
	stack.postgresFailures[resourceID] = err
	stack.mu.Unlock()
}
