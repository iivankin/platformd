package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

// Delete serializes runtime lifecycle work through the resource lock and uses
// maintenance admission to drain queries before stopping the database.
func (controller *Controller) Delete(ctx context.Context, input state.DeleteResourceInput) (deleted state.ManagedPostgres, resultErr error) {
	lease, err := controller.admission.Begin("postgres_delete", input.ID)
	if err != nil {
		return state.ManagedPostgres{}, err
	}
	defer lease.Release()
	lock := controller.resourceLock(input.ID)
	lock.Lock()
	defer lock.Unlock()

	resource, err := controller.store.ManagedPostgres(ctx, input.ID)
	if err != nil {
		return state.ManagedPostgres{}, err
	}
	if resource.ProjectID != input.ProjectID {
		return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
	}
	if resource.UpdatedAtMillis != input.ExpectedUpdatedMillis {
		return state.ManagedPostgres{}, state.ErrManagedPostgresChanged
	}
	active, wasActive := controller.activeRuntime(input.ID)
	releaseMaintenance, err := controller.prepareDeletionMaintenance(ctx, active, wasActive)
	if err != nil {
		return state.ManagedPostgres{}, err
	}
	committed := false
	if releaseMaintenance != nil {
		defer func() {
			releaseErr := releaseMaintenance()
			if releaseErr == nil {
				return
			}
			wrapped := fmt.Errorf("release managed PostgreSQL %s deletion maintenance: %w", input.ID, releaseErr)
			if committed {
				controller.onCleanupError(wrapped)
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
		}()
	}
	stopContext, cancelStop := context.WithTimeout(context.WithoutCancel(ctx), controller.readyTimeout+30*time.Second)
	stopErr := controller.stopLocked(stopContext, input.ID)
	cancelStop()
	if stopErr != nil {
		return state.ManagedPostgres{}, errors.Join(stopErr, controller.restartAfterDeleteFailure(ctx, input.ID, wasActive))
	}
	deleted, err = controller.store.DeleteManagedPostgres(ctx, input)
	if err != nil {
		return state.ManagedPostgres{}, errors.Join(err, controller.restartAfterDeleteFailure(ctx, input.ID, wasActive))
	}
	committed = true
	cleanupContext, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), controller.readyTimeout+30*time.Second)
	defer cancelCleanup()
	if err := controller.removeManagedPostgresVolume(cleanupContext, deleted.ProjectID, deleted.VolumeID); err != nil {
		controller.onCleanupError(fmt.Errorf("remove deleted managed PostgreSQL %s storage: %w", deleted.ID, err))
	}
	return deleted, nil
}

func (controller *Controller) prepareDeletionMaintenance(
	ctx context.Context,
	active activeRuntime,
	wasActive bool,
) (func() error, error) {
	if !wasActive {
		return nil, nil
	}
	endpoint, err := postgresVersionEndpoint(controller.engine, active)
	if err != nil {
		return nil, err
	}
	ownerPassword, err := controller.ownerPassword(active.resource)
	if err != nil {
		return nil, fmt.Errorf("open managed PostgreSQL owner password before deletion: %w", err)
	}
	if !controller.beginMaintenance(active.resource.ID) {
		return nil, ErrMaintenance
	}
	releaseFirewall, err := controller.maintenance.BlockDatabase(
		ctx, active.resource.ProjectID, endpoint, Port,
	)
	if err != nil {
		controller.endMaintenance(active.resource.ID)
		return nil, fmt.Errorf("block new managed PostgreSQL connections before deletion: %w", err)
	}
	release := func() error {
		releaseErr := releaseFirewall()
		controller.endMaintenance(active.resource.ID)
		return releaseErr
	}
	if err := controller.drainAndTerminateClients(ctx, active, ownerPassword); err != nil {
		return nil, errors.Join(fmt.Errorf("drain managed PostgreSQL clients before deletion: %w", err), release())
	}
	return release, nil
}

func (controller *Controller) restartAfterDeleteFailure(ctx context.Context, resourceID string, wasActive bool) error {
	if !wasActive {
		return nil
	}
	recoveryContext, cancelRecovery := context.WithTimeout(context.WithoutCancel(ctx), controller.readyTimeout+30*time.Second)
	defer cancelRecovery()
	return controller.startLocked(recoveryContext, resourceID)
}
