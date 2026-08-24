package managedpostgres

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

type deletionStore struct {
	resource           state.ManagedPostgres
	cancel             context.CancelFunc
	deleteErr          error
	recoveryErr        error
	reads              int
	recoveryContextErr error
}

func (store *deletionStore) ManagedPostgres(ctx context.Context, id string) (state.ManagedPostgres, error) {
	store.reads++
	if store.reads > 1 {
		store.recoveryContextErr = ctx.Err()
		return state.ManagedPostgres{}, store.recoveryErr
	}
	if id != store.resource.ID {
		return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
	}
	return store.resource, nil
}

func (store *deletionStore) ManagedPostgresResources(context.Context) ([]state.ManagedPostgres, error) {
	return []state.ManagedPostgres{store.resource}, nil
}

func (*deletionStore) SwitchManagedPostgresVolume(context.Context, state.SwitchManagedPostgresVolume) error {
	return nil
}

func (store *deletionStore) DeleteManagedPostgres(context.Context, state.DeleteResourceInput) (state.ManagedPostgres, error) {
	store.cancel()
	return state.ManagedPostgres{}, store.deleteErr
}

func TestDeleteRestartsActivePostgresAfterRequestCancellation(t *testing.T) {
	t.Parallel()
	deleteErr := errors.New("delete failed")
	recoveryErr := errors.New("recovery reached store")
	requestContext, cancelRequest := context.WithCancel(context.Background())
	store := &deletionStore{
		resource: state.ManagedPostgres{
			ID: "postgres-id", ProjectID: "project-id", VolumeID: "volume-id", UpdatedAtMillis: 7,
		},
		cancel: cancelRequest, deleteErr: deleteErr, recoveryErr: recoveryErr,
	}
	engine := &postgresRestoreEngine{containers: map[string]containerengine.Container{
		"container-id": {ID: "container-id", State: "running", IPs: map[string][]string{"network": {"10.90.0.5"}}},
	}}
	controller := &Controller{
		store: store, engine: engine, publisher: &postgresRestorePublisher{}, admission: admission.New(),
		volumeRoot: filepath.Join(t.TempDir(), "volumes"), readyTimeout: time.Second,
		onCleanupError: func(error) {}, maintenance: allowMaintenanceGate{}, maintenanceDrain: time.Nanosecond,
		ownerPassword: func(state.ManagedPostgres) (string, error) { return "owner-password", nil },
		dial: func(context.Context, string, string, string, string) (Connection, error) {
			return &postgresRestoreConnection{}, nil
		},
		active: map[string]activeRuntime{
			"postgres-id": {resource: store.resource, container: engine.containers["container-id"], network: "network"},
		},
		maintaining: make(map[string]struct{}),
	}

	_, err := controller.Delete(requestContext, state.DeleteResourceInput{
		ID: "postgres-id", ProjectID: "project-id", ExpectedUpdatedMillis: 7,
	})
	if !errors.Is(err, deleteErr) || !errors.Is(err, recoveryErr) {
		t.Fatalf("delete error = %v", err)
	}
	if store.recoveryContextErr != nil {
		t.Fatalf("recovery inherited canceled request context: %v", store.recoveryContextErr)
	}
	if engine.removeContext != nil {
		t.Fatalf("container removal inherited canceled request context: %v", engine.removeContext)
	}
}
