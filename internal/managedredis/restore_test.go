package managedredis

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

type restoreStore struct {
	resource    state.ManagedRedis
	switchInput state.SwitchManagedRedisVolume
	switchErr   error
}

func (store *restoreStore) ManagedRedis(_ context.Context, id string) (state.ManagedRedis, error) {
	if id != store.resource.ID {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	return store.resource, nil
}

func (store *restoreStore) ManagedRedisResources(context.Context) ([]state.ManagedRedis, error) {
	return []state.ManagedRedis{store.resource}, nil
}

func (store *restoreStore) SwitchManagedRedisVolume(_ context.Context, input state.SwitchManagedRedisVolume) error {
	store.switchInput = input
	if store.switchErr != nil {
		return store.switchErr
	}
	if input.ResourceID != store.resource.ID || input.ExpectedVolumeID != store.resource.VolumeID {
		return errors.New("unexpected managed Redis volume switch")
	}
	store.resource.VolumeID = input.VolumeID
	store.resource.UpdatedAtMillis = input.UpdatedAtMillis
	return nil
}

type restoreEngine struct {
	image      containerengine.Image
	containers map[string]containerengine.Container
	created    []containerengine.ContainerSpec
	started    []string
	stopped    []string
	removed    []string
}

func (engine *restoreEngine) Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error) {
	return containerengine.Image{}, errors.New("restore unexpectedly pulled a cached image")
}

func (engine *restoreEngine) InspectImage(context.Context, string) (containerengine.Image, error) {
	return engine.image, nil
}

func (engine *restoreEngine) CreateContainer(_ context.Context, spec containerengine.ContainerSpec) (containerengine.Container, error) {
	engine.created = append(engine.created, spec)
	container := containerengine.Container{
		ID: spec.Name + "-container", State: "created",
		IPs: map[string][]string{spec.Network: {"10.90.0.5"}},
	}
	engine.containers[container.ID] = container
	return container, nil
}

func (engine *restoreEngine) StartContainer(_ context.Context, id string) error {
	container, exists := engine.containers[id]
	if !exists {
		return errors.New("start unknown container")
	}
	container.State = "running"
	engine.containers[id] = container
	engine.started = append(engine.started, id)
	return nil
}

func (engine *restoreEngine) StopContainer(id string, _ uint) error {
	container, exists := engine.containers[id]
	if !exists {
		return errors.New("stop unknown container")
	}
	container.State = "stopped"
	engine.containers[id] = container
	engine.stopped = append(engine.stopped, id)
	return nil
}

func (engine *restoreEngine) RemoveContainer(_ context.Context, id string, _ bool) error {
	if _, exists := engine.containers[id]; !exists {
		return errors.New("remove unknown container")
	}
	delete(engine.containers, id)
	engine.removed = append(engine.removed, id)
	return nil
}

func (engine *restoreEngine) InspectContainer(id string) (containerengine.Container, error) {
	container, exists := engine.containers[id]
	if !exists {
		return containerengine.Container{}, errors.New("inspect unknown container")
	}
	return container, nil
}

type restorePublisher struct{ events []string }

func (publisher *restorePublisher) PublishRedis(_ state.ManagedRedis, container containerengine.Container) error {
	publisher.events = append(publisher.events, "publish:"+container.ID)
	return nil
}

func (publisher *restorePublisher) WithdrawRedis(resource state.ManagedRedis) error {
	publisher.events = append(publisher.events, "withdraw:"+resource.ID)
	return nil
}

func TestRestoreReplacePublishesValidatedCandidateAndDeletesOldVolume(t *testing.T) {
	t.Parallel()
	fixture := newRestoreFixture(t, nil)
	backup := []byte("REDIS0011-restored-rdb")
	if err := fixture.controller.RestoreReplace(context.Background(), "redis-id", bytes.NewReader(backup), Actor{
		Kind: "access", ID: "user", Email: "user@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	if fixture.store.resource.VolumeID != "new-volume" || fixture.store.switchInput.Action != "redis.restore" ||
		fixture.store.switchInput.AuditEventID != "audit-id" || fixture.store.switchInput.RequestCorrelationID != "correlation-id" {
		t.Fatalf("volume switch = %+v, resource = %+v", fixture.store.switchInput, fixture.store.resource)
	}
	newRDB, err := os.ReadFile(filepath.Join(fixture.volumeRoot, "project-id", "new-volume", "dump.rdb"))
	if err != nil || !bytes.Equal(newRDB, backup) {
		t.Fatalf("restored RDB = %q, %v", newRDB, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "old-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old volume still exists: %v", err)
	}
	candidateID := "platformd-redis-runtime-id-container"
	active, exists := fixture.controller.activeRuntime("redis-id")
	if !exists || active.container.ID != candidateID || active.resource.VolumeID != "new-volume" {
		t.Fatalf("active runtime = %+v, %v", active, exists)
	}
	if _, exists := fixture.engine.containers["old-container"]; exists {
		t.Fatal("old container was not removed")
	}
	if len(fixture.engine.created) != 1 {
		t.Fatalf("created specs = %d", len(fixture.engine.created))
	}
	spec := fixture.engine.created[0]
	if spec.Name != "platformd-redis-runtime-id" || spec.Labels["io.platformd.redis-id"] != "redis-id" ||
		!strings.HasSuffix(spec.LogPath, "/redis/redis-id/attempt-id.log") {
		t.Fatalf("candidate identity/profile = %+v", spec)
	}
	if !reflect.DeepEqual(fixture.publisher.events, []string{"withdraw:redis-id", "publish:" + candidateID}) {
		t.Fatalf("publication events = %v", fixture.publisher.events)
	}
}

func TestRestoreReplaceRestartsOldRuntimeWhenPointerSwitchFails(t *testing.T) {
	t.Parallel()
	fixture := newRestoreFixture(t, errors.New("forced switch failure"))
	err := fixture.controller.RestoreReplace(context.Background(), "redis-id", strings.NewReader("REDIS0011-rdb"), Actor{
		Kind: "token", ID: "token",
	})
	if err == nil || !strings.Contains(err.Error(), "forced switch failure") {
		t.Fatalf("restore error = %v", err)
	}
	if fixture.store.resource.VolumeID != "old-volume" {
		t.Fatalf("active volume = %q", fixture.store.resource.VolumeID)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "old-volume", "dump.rdb")); err != nil {
		t.Fatalf("old volume was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "new-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed candidate volume still exists: %v", err)
	}
	old, exists := fixture.engine.containers["old-container"]
	if !exists || old.State != "running" {
		t.Fatalf("old container = %+v, %v", old, exists)
	}
	if _, exists := fixture.engine.containers["platformd-redis-runtime-id-container"]; exists {
		t.Fatal("failed candidate container was not removed")
	}
	if !reflect.DeepEqual(fixture.publisher.events, []string{"withdraw:redis-id", "publish:old-container"}) {
		t.Fatalf("rollback publication events = %v", fixture.publisher.events)
	}
}

type restoreFixture struct {
	controller *Controller
	store      *restoreStore
	engine     *restoreEngine
	publisher  *restorePublisher
	volumeRoot string
}

func newRestoreFixture(t *testing.T, switchErr error) restoreFixture {
	t.Helper()
	root := t.TempDir()
	generatedRoot := filepath.Join(root, "generated")
	if err := os.Mkdir(generatedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	volumeRoot := filepath.Join(root, "volumes")
	oldVolume, err := ensureVolume(volumeRoot, "project-id", "old-volume")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldVolume, "dump.rdb"), []byte("old-rdb"), 0o600); err != nil {
		t.Fatal(err)
	}
	password, err := GeneratePasswordWith(zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	resource := state.ManagedRedis{
		ID: "redis-id", ProjectID: "project-id", ProjectName: "shop", Name: "cache",
		ImageTag: "7.4", ImageDigest: testImageDigest, VolumeID: "old-volume",
	}
	store := &restoreStore{resource: resource, switchErr: switchErr}
	engine := &restoreEngine{
		image: containerengine.Image{ID: "image-id", Digest: testImageDigest},
		containers: map[string]containerengine.Container{
			"old-container": {
				ID: "old-container", State: "running",
				IPs: map[string][]string{"network": {"10.90.0.4"}},
			},
		},
	}
	publisher := &restorePublisher{}
	ids := []string{"new-volume", "runtime-id", "audit-id", "correlation-id", "attempt-id"}
	controller, err := NewController(Config{
		Store: store, Engine: engine, Publisher: publisher, Growth: allowGrowthGate{}, Admission: admission.New(),
		Password: func(state.ManagedRedis) (string, error) { return password, nil },
		Placement: func(state.ManagedRedis) (Placement, error) {
			return Placement{
				NetworkName: "network", Gateway: netip.MustParseAddr("10.90.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/workload/redis-id",
			}, nil
		},
		Dial:          func(context.Context, string, string) (RedisConnection, error) { return &testConnection{}, nil },
		GeneratedRoot: generatedRoot, VolumeRoot: volumeRoot, LogRoot: filepath.Join(root, "logs"),
		LogSizeBytes: 1 << 20, LogMaxFiles: 3, ReadyTimeout: time.Second, ProbePeriod: time.Millisecond,
		Now: func() time.Time { return time.UnixMilli(10) },
		NewID: func(time.Time) (string, error) {
			if len(ids) == 0 {
				return "", errors.New("unexpected ID allocation")
			}
			next := ids[0]
			ids = ids[1:]
			return next, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.setActive(resource.ID, activeRuntime{
		resource: resource, container: engine.containers["old-container"], network: "network", runtimeID: resource.ID,
	})
	return restoreFixture{
		controller: controller, store: store, engine: engine, publisher: publisher, volumeRoot: volumeRoot,
	}
}
