package managedredis

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

type deletionMaintenanceGate struct {
	controller  *Controller
	mutationErr error
	released    bool
}

func (gate *deletionMaintenanceGate) BlockDatabase(
	ctx context.Context,
	_ string,
	_ netip.Addr,
	_ uint16,
) (func() error, error) {
	_, gate.mutationErr = gate.controller.Mutate(ctx, "redis-id", Mutation{
		Kind: MutationKeyDelete, Key: []byte("during-delete"),
	})
	return func() error {
		gate.released = true
		return nil
	}, nil
}

type deletionStore struct {
	resource           state.ManagedRedis
	cancel             context.CancelFunc
	deleteErr          error
	recoveryErr        error
	reads              int
	recoveryContextErr error
}

func (store *deletionStore) ManagedRedis(ctx context.Context, id string) (state.ManagedRedis, error) {
	store.reads++
	if store.reads > 1 {
		store.recoveryContextErr = ctx.Err()
		return state.ManagedRedis{}, store.recoveryErr
	}
	if id != store.resource.ID {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	return store.resource, nil
}

func (store *deletionStore) ManagedRedisResources(context.Context) ([]state.ManagedRedis, error) {
	return []state.ManagedRedis{store.resource}, nil
}

func (*deletionStore) SwitchManagedRedisVolume(context.Context, state.SwitchManagedRedisVolume) error {
	return nil
}

func (store *deletionStore) DeleteManagedRedis(context.Context, state.DeleteResourceInput) (state.ManagedRedis, error) {
	store.cancel()
	return state.ManagedRedis{}, store.deleteErr
}

func TestDeleteRestartsActiveRedisAfterRequestCancellation(t *testing.T) {
	t.Parallel()
	deleteErr := errors.New("delete failed")
	recoveryErr := errors.New("recovery reached store")
	requestContext, cancelRequest := context.WithCancel(context.Background())
	store := &deletionStore{
		resource: state.ManagedRedis{
			ID: "redis-id", ProjectID: "project-id", VolumeID: "volume-id", UpdatedAtMillis: 7,
		},
		cancel: cancelRequest, deleteErr: deleteErr, recoveryErr: recoveryErr,
	}
	engine := &restoreEngine{containers: map[string]containerengine.Container{
		"container-id": {ID: "container-id", State: "running", IPs: map[string][]string{"network": {"10.90.0.5"}}},
	}}
	maintenance := &deletionMaintenanceGate{}
	controller := &Controller{
		store: store, engine: engine, publisher: &restorePublisher{}, admission: admission.New(),
		generatedRoot: filepath.Join(t.TempDir(), "generated"), volumeRoot: filepath.Join(t.TempDir(), "volumes"),
		readyTimeout: time.Second, onCleanupError: func(error) {}, maintenance: maintenance, maintenanceDrain: time.Nanosecond,
		password: func(state.ManagedRedis) (string, error) { return "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", nil },
		dial:     func(context.Context, string, string) (RedisConnection, error) { return &testConnection{}, nil },
		active: map[string]activeRuntime{
			"redis-id": {resource: store.resource, container: engine.containers["container-id"], network: "network"},
		},
		maintaining: make(map[string]struct{}),
	}
	maintenance.controller = controller

	_, err := controller.Delete(requestContext, state.DeleteResourceInput{
		ID: "redis-id", ProjectID: "project-id", ExpectedUpdatedMillis: 7,
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
	if !errors.Is(maintenance.mutationErr, ErrMaintenance) || !maintenance.released {
		t.Fatalf("mutation during deletion = %v, maintenance released = %t", maintenance.mutationErr, maintenance.released)
	}
}
