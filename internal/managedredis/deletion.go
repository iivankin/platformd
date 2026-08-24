package managedredis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

// Delete serializes runtime lifecycle work through the resource lock and uses
// maintenance admission to drain mutations before taking the final snapshot.
func (controller *Controller) Delete(ctx context.Context, input state.DeleteResourceInput) (deleted state.ManagedRedis, resultErr error) {
	lease, err := controller.admission.Begin("redis_delete", input.ID)
	if err != nil {
		return state.ManagedRedis{}, err
	}
	defer lease.Release()
	lock := controller.resourceLock(input.ID)
	lock.Lock()
	defer lock.Unlock()

	resource, err := controller.store.ManagedRedis(ctx, input.ID)
	if err != nil {
		return state.ManagedRedis{}, err
	}
	if resource.ProjectID != input.ProjectID {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	if resource.UpdatedAtMillis != input.ExpectedUpdatedMillis {
		return state.ManagedRedis{}, state.ErrManagedRedisChanged
	}
	active, wasActive := controller.activeRuntime(input.ID)
	releaseMaintenance, err := controller.prepareDeletionMaintenance(ctx, active, wasActive)
	if err != nil {
		return state.ManagedRedis{}, err
	}
	committed := false
	if releaseMaintenance != nil {
		defer func() {
			releaseErr := releaseMaintenance()
			if releaseErr == nil {
				return
			}
			wrapped := fmt.Errorf("release managed Redis %s deletion maintenance: %w", input.ID, releaseErr)
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
		return state.ManagedRedis{}, errors.Join(stopErr, controller.restartAfterDeleteFailure(ctx, input.ID, wasActive))
	}
	deleted, err = controller.store.DeleteManagedRedis(ctx, input)
	if err != nil {
		return state.ManagedRedis{}, errors.Join(err, controller.restartAfterDeleteFailure(ctx, input.ID, wasActive))
	}
	committed = true
	cleanupContext, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), controller.readyTimeout+30*time.Second)
	defer cancelCleanup()
	cleanupErr := controller.removeManagedRedisVolume(cleanupContext, deleted.ProjectID, deleted.VolumeID)
	cleanupErr = errors.Join(cleanupErr, removeManagedRedisConfig(controller.generatedRoot, deleted.ID))
	if wasActive && active.runtimeID != deleted.ID {
		cleanupErr = errors.Join(cleanupErr, removeManagedRedisConfig(controller.generatedRoot, active.runtimeID))
	}
	if cleanupErr != nil {
		controller.onCleanupError(fmt.Errorf("remove deleted managed Redis %s storage: %w", deleted.ID, cleanupErr))
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
	endpoint, err := redisVersionEndpoint(controller.engine, active)
	if err != nil {
		return nil, err
	}
	password, err := controller.password(active.resource)
	if err != nil {
		return nil, fmt.Errorf("open managed Redis password before deletion: %w", err)
	}
	if !controller.beginMaintenance(active.resource.ID) {
		return nil, ErrMaintenance
	}
	releaseFirewall, err := controller.maintenance.BlockDatabase(
		ctx, active.resource.ProjectID, endpoint, Port,
	)
	if err != nil {
		controller.endMaintenance(active.resource.ID)
		return nil, fmt.Errorf("block new managed Redis connections before deletion: %w", err)
	}
	release := func() error {
		releaseErr := releaseFirewall()
		controller.endMaintenance(active.resource.ID)
		return releaseErr
	}
	if err := controller.drainAndKillClients(ctx, active, password); err != nil {
		return nil, errors.Join(fmt.Errorf("drain managed Redis clients before deletion: %w", err), release())
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
