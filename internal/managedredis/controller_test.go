package managedredis

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

const testImageDigest = "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4"

type testStore struct {
	resource state.ManagedRedis
}

func (store testStore) ManagedRedis(_ context.Context, id string) (state.ManagedRedis, error) {
	if id != store.resource.ID {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	return store.resource, nil
}

func (store testStore) ManagedRedisResources(context.Context) ([]state.ManagedRedis, error) {
	return []state.ManagedRedis{store.resource}, nil
}

type testEngine struct {
	image       containerengine.Image
	pullRequest containerengine.PullRequest
	created     containerengine.ContainerSpec
	container   containerengine.Container
	started     bool
	stopped     bool
	removed     bool
}

func (engine *testEngine) Pull(_ context.Context, request containerengine.PullRequest) (containerengine.Image, error) {
	engine.pullRequest = request
	return engine.image, nil
}

func (engine *testEngine) InspectImage(context.Context, string) (containerengine.Image, error) {
	return containerengine.Image{}, errors.New("image not cached")
}

func (engine *testEngine) CreateContainer(_ context.Context, spec containerengine.ContainerSpec) (containerengine.Container, error) {
	engine.created = spec
	return engine.container, nil
}

func (engine *testEngine) StartContainer(context.Context, string) error {
	engine.started = true
	return nil
}

func (engine *testEngine) StopContainer(string, uint) error {
	engine.stopped = true
	return nil
}

func (engine *testEngine) RemoveContainer(context.Context, string, bool) error {
	engine.removed = true
	return nil
}

func (engine *testEngine) InspectContainer(string) (containerengine.Container, error) {
	if !engine.started {
		return containerengine.Container{}, errors.New("container was inspected before start")
	}
	return engine.container, nil
}

type testConnection struct {
	pinged bool
	saved  bool
	closed bool
}

func (connection *testConnection) Ping(context.Context) error {
	connection.pinged = true
	return nil
}

func (connection *testConnection) Save(context.Context) error {
	connection.saved = true
	return nil
}

func (*testConnection) ScanKeys(context.Context, ScanQuery) (KeyPage, error) {
	return KeyPage{}, nil
}

func (*testConnection) PreviewKey(context.Context, PreviewQuery) (Preview, error) {
	return Preview{}, nil
}

func (*testConnection) Mutate(context.Context, Mutation) (MutationResult, error) {
	return MutationResult{Affected: 1}, nil
}

func (connection *testConnection) Close() error {
	connection.closed = true
	return nil
}

type testPublisher struct {
	published bool
	withdrawn bool
}

func (publisher *testPublisher) PublishRedis(_ state.ManagedRedis, _ containerengine.Container) error {
	publisher.published = true
	return nil
}

func (publisher *testPublisher) WithdrawRedis(state.ManagedRedis) error {
	publisher.withdrawn = true
	return nil
}

func TestControllerStartsPinnedProfileAfterAuthenticatedReadinessAndFinalSave(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	generatedRoot := filepath.Join(root, "generated")
	if err := os.Mkdir(generatedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	password, err := GeneratePasswordWith(zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	resource := state.ManagedRedis{
		ID: "redis-id", ProjectID: "project-id", ProjectName: "shop", Name: "cache",
		ImageTag: "7.4", ImageDigest: testImageDigest, VolumeID: "volume-id",
		CPUMillicores: 250, MemoryMaxBytes: 128 << 20,
	}
	engine := &testEngine{
		image: containerengine.Image{ID: "image-id", Digest: testImageDigest},
		container: containerengine.Container{
			ID: "container-id", State: "running", IPs: map[string][]string{"network": {"10.90.0.4"}},
		},
	}
	publisher := &testPublisher{}
	connections := make([]*testConnection, 0, 2)
	controller, err := NewController(Config{
		Store: testStore{resource: resource}, Engine: engine, Publisher: publisher, Growth: allowGrowthGate{},
		Password: func(state.ManagedRedis) (string, error) { return password, nil },
		Placement: func(state.ManagedRedis) (Placement, error) {
			return Placement{NetworkName: "network", Gateway: netip.MustParseAddr("10.90.0.1"), DNSSearch: "shop.internal", CgroupParent: "/workload/redis-id"}, nil
		},
		Dial: func(_ context.Context, address, actualPassword string) (RedisConnection, error) {
			if address != "10.90.0.4:6379" || actualPassword != password {
				t.Fatalf("dial = %q/%q", address, actualPassword)
			}
			connection := &testConnection{}
			connections = append(connections, connection)
			return connection, nil
		},
		GeneratedRoot: generatedRoot, VolumeRoot: filepath.Join(root, "volumes"),
		LogRoot: filepath.Join(root, "logs"), LogSizeBytes: 1 << 20, LogMaxFiles: 3,
		ReadyTimeout: time.Second, ProbePeriod: time.Millisecond,
		NewID: func(time.Time) (string, error) { return "attempt-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(context.Background(), resource.ID); err != nil {
		t.Fatal(err)
	}
	if !engine.started || !publisher.published || len(connections) != 1 || !connections[0].pinged || !connections[0].closed {
		t.Fatalf("runtime was published before complete readiness: engine=%+v publisher=%+v connections=%+v", engine, publisher, connections)
	}
	if engine.pullRequest.Reference != "docker.io/library/redis@"+testImageDigest || engine.pullRequest.Refresh {
		t.Fatalf("pull request = %+v", engine.pullRequest)
	}
	if !reflect.DeepEqual(engine.created.Command, []string{"redis-server", "/run/platformd/redis.conf"}) || engine.created.CPUMillicores != 250 || engine.created.MemoryMaxBytes != 128<<20 {
		t.Fatalf("container profile = %+v", engine.created)
	}
	if len(engine.created.Mounts) != 2 || engine.created.Mounts[0].Destination != "/data" || engine.created.Mounts[0].ReadOnly || !engine.created.Mounts[1].ReadOnly {
		t.Fatalf("container mounts = %+v", engine.created.Mounts)
	}
	configPath := filepath.Join(generatedRoot, resource.ID, "redis.conf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	wantConfig := []byte("daemonize no\nbind 0.0.0.0\nprotected-mode yes\nport 6379\ndir /data\ndbfilename dump.rdb\nappendonly no\nsave 300 1\nrequirepass " + password + "\n")
	if !reflect.DeepEqual(config, wantConfig) {
		t.Fatalf("redis.conf = %q", config)
	}
	for path, mode := range map[string]os.FileMode{filepath.Dir(configPath): 0o700, configPath: 0o444} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("mode %s = %v, want %v", path, info.Mode().Perm(), mode)
		}
	}
	if err := controller.Stop(context.Background(), resource.ID); err != nil {
		t.Fatal(err)
	}
	if !publisher.withdrawn || !engine.stopped || !engine.removed || len(connections) != 2 || !connections[1].saved || !connections[1].closed {
		t.Fatalf("graceful stop incomplete: engine=%+v publisher=%+v connections=%+v", engine, publisher, connections)
	}
}

func TestControllerDoesNotPublishAndRemovesFailedReadinessCandidate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	generatedRoot := filepath.Join(root, "generated")
	if err := os.Mkdir(generatedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	password, err := GeneratePasswordWith(zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	resource := state.ManagedRedis{
		ID: "redis-id", ProjectID: "project-id", ProjectName: "shop", Name: "cache",
		ImageTag: "latest", ImageDigest: testImageDigest, VolumeID: "volume-id",
	}
	engine := &testEngine{
		image:     containerengine.Image{ID: "image-id", Digest: testImageDigest},
		container: containerengine.Container{ID: "container-id", State: "running", IPs: map[string][]string{"network": {"10.90.0.4"}}},
	}
	publisher := &testPublisher{}
	controller, err := NewController(Config{
		Store: testStore{resource: resource}, Engine: engine, Publisher: publisher, Growth: allowGrowthGate{},
		Password: func(state.ManagedRedis) (string, error) { return password, nil },
		Placement: func(state.ManagedRedis) (Placement, error) {
			return Placement{NetworkName: "network", Gateway: netip.MustParseAddr("10.90.0.1")}, nil
		},
		Dial:          func(context.Context, string, string) (RedisConnection, error) { return nil, errors.New("AUTH failed") },
		GeneratedRoot: generatedRoot, VolumeRoot: filepath.Join(root, "volumes"), LogRoot: filepath.Join(root, "logs"),
		LogSizeBytes: 1 << 20, LogMaxFiles: 3, ReadyTimeout: 3 * time.Millisecond, ProbePeriod: time.Millisecond,
		NewID: func(time.Time) (string, error) { return "attempt-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(context.Background(), resource.ID); err == nil {
		t.Fatal("failed readiness was accepted")
	}
	if publisher.published || !engine.removed {
		t.Fatalf("failed candidate publication/removal = %v/%v", publisher.published, engine.removed)
	}
}

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	clear(value)
	return len(value), nil
}
