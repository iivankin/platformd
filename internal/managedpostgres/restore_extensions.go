package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

// restoreExtensionNames snapshots extension state before a candidate is built.
// pg_dump records CREATE EXTENSION statements, but the managed database owner is
// intentionally not a superuser. Creating the same extensions in the unpublished
// candidate first lets pg_restore retain its normal owner credentials safely.
func (controller *Controller) restoreExtensionNames(
	ctx context.Context,
	resourceID string,
	source activeRuntime,
	sourceRunning bool,
) ([]string, error) {
	names := make(map[string]struct{})
	if sourceRunning {
		connection, err := controller.bootstrapConnection(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("open source PostgreSQL extensions: %w", err)
		}
		extensions, listErr := connection.Extensions(ctx)
		closeErr := connection.Close(ctx)
		if listErr != nil || closeErr != nil {
			return nil, fmt.Errorf("list source PostgreSQL extensions: %w", errors.Join(listErr, closeErr))
		}
		for _, extension := range extensions {
			if extension.InstalledVersion != "" {
				names[extension.Name] = struct{}{}
			}
		}
	}
	if controller.extensions != nil {
		managed, err := controller.extensions.ManagedPostgresExtensions(ctx, resourceID)
		if err != nil {
			return nil, fmt.Errorf("load managed PostgreSQL extensions for restore: %w", err)
		}
		for _, extension := range managed {
			names[extension.Name] = struct{}{}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func (controller *Controller) prepareRestoreExtensions(
	ctx context.Context,
	resource state.ManagedPostgres,
	candidate containerengine.Container,
	networkName string,
	names []string,
) error {
	if len(names) == 0 {
		return nil
	}
	connection, err := controller.bootstrapConnection(ctx, activeRuntime{
		resource: resource, container: candidate, network: networkName,
	})
	if err != nil {
		return fmt.Errorf("open restore candidate extensions: %w", err)
	}
	var changeErr error
	for _, name := range names {
		if err := connection.ChangeExtension(ctx, name, true); err != nil {
			changeErr = errors.Join(changeErr, fmt.Errorf("create extension %q: %w", name, err))
			break
		}
	}
	if err := errors.Join(changeErr, connection.Close(ctx)); err != nil {
		return fmt.Errorf("prepare PostgreSQL restore extensions: %w", err)
	}
	return nil
}
