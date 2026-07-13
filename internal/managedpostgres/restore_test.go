package managedpostgres

import (
	"bytes"
	"context"
	"errors"
	"io"
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

const restoreTestImageDigest = "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4"

type postgresRestoreStore struct {
	resource    state.ManagedPostgres
	switchInput state.SwitchManagedPostgresVolume
	switchErr   error
}

func (store *postgresRestoreStore) ManagedPostgres(_ context.Context, id string) (state.ManagedPostgres, error) {
	if id != store.resource.ID {
		return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
	}
	return store.resource, nil
}

func (store *postgresRestoreStore) ManagedPostgresResources(context.Context) ([]state.ManagedPostgres, error) {
	return []state.ManagedPostgres{store.resource}, nil
}

func (store *postgresRestoreStore) SwitchManagedPostgresVolume(_ context.Context, input state.SwitchManagedPostgresVolume) error {
	store.switchInput = input
	if store.switchErr != nil {
		return store.switchErr
	}
	if input.ResourceID != store.resource.ID || input.ExpectedVolumeID != store.resource.VolumeID {
		return errors.New("unexpected managed PostgreSQL volume switch")
	}
	store.resource.VolumeID = input.VolumeID
	if input.Action == "postgres.version_change" {
		store.resource.ImageTag = input.ImageTag
		store.resource.ImageDigest = input.ImageDigest
	}
	store.resource.UpdatedAtMillis = input.UpdatedAtMillis
	return nil
}

type postgresRestoreEngine struct {
	image       containerengine.Image
	containers  map[string]containerengine.Container
	created     []containerengine.ContainerSpec
	started     []string
	stopped     []string
	removed     []string
	execRequest containerengine.ExecRequest
	execPayload []byte
	execCode    int
	execErr     error
	dumpRequest containerengine.ExecRequest
	dumpPayload []byte
	dumpCode    int
	dumpErr     error
}

func (*postgresRestoreEngine) Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error) {
	return containerengine.Image{}, errors.New("restore unexpectedly pulled a cached image")
}

func (engine *postgresRestoreEngine) InspectImage(context.Context, string) (containerengine.Image, error) {
	return engine.image, nil
}

func (engine *postgresRestoreEngine) CreateContainer(_ context.Context, spec containerengine.ContainerSpec) (containerengine.Container, error) {
	engine.created = append(engine.created, spec)
	container := containerengine.Container{
		ID: spec.Name + "-container", State: "created",
		IPs: map[string][]string{spec.Network: {"10.90.0.5"}},
	}
	engine.containers[container.ID] = container
	return container, nil
}

func (engine *postgresRestoreEngine) StartContainer(_ context.Context, id string) error {
	container, exists := engine.containers[id]
	if !exists {
		return errors.New("start unknown container")
	}
	container.State = "running"
	engine.containers[id] = container
	engine.started = append(engine.started, id)
	return nil
}

func (engine *postgresRestoreEngine) StopContainer(id string, _ uint) error {
	container, exists := engine.containers[id]
	if !exists {
		return errors.New("stop unknown container")
	}
	container.State = "stopped"
	engine.containers[id] = container
	engine.stopped = append(engine.stopped, id)
	return nil
}

func (engine *postgresRestoreEngine) RemoveContainer(_ context.Context, id string, _ bool) error {
	if _, exists := engine.containers[id]; !exists {
		return errors.New("remove unknown container")
	}
	delete(engine.containers, id)
	engine.removed = append(engine.removed, id)
	return nil
}

func (engine *postgresRestoreEngine) InspectContainer(id string) (containerengine.Container, error) {
	container, exists := engine.containers[id]
	if !exists {
		return containerengine.Container{}, errors.New("inspect unknown container")
	}
	return container, nil
}

func (engine *postgresRestoreEngine) ExecContainer(_ context.Context, _ string, request containerengine.ExecRequest) (int, error) {
	if len(request.Command) > 0 && request.Command[0] == "pg_dump" {
		engine.dumpRequest = request
		if request.Stdout == nil {
			return -1, errors.New("pg_dump stdout is missing")
		}
		if _, err := request.Stdout.Write(engine.dumpPayload); err != nil {
			return -1, err
		}
		if request.Stderr != nil && engine.dumpErr != nil {
			_, _ = io.WriteString(request.Stderr, engine.dumpErr.Error())
		}
		return engine.dumpCode, engine.dumpErr
	}
	engine.execRequest = request
	if request.Stdin == nil {
		return -1, errors.New("pg_restore stdin is missing")
	}
	payload, err := io.ReadAll(request.Stdin)
	if err != nil {
		return -1, err
	}
	engine.execPayload = payload
	if request.Stderr != nil && engine.execErr != nil {
		_, _ = io.WriteString(request.Stderr, engine.execErr.Error())
	}
	return engine.execCode, engine.execErr
}

type postgresRestoreConnection struct {
	bootstrapCalls int
	pingCalls      int
	closeCalls     int
}

func (connection *postgresRestoreConnection) Bootstrap(context.Context, string, string, string) error {
	connection.bootstrapCalls++
	return nil
}

func (connection *postgresRestoreConnection) Ping(context.Context) error {
	connection.pingCalls++
	return nil
}

func (*postgresRestoreConnection) Query(context.Context, string) (QueryResult, error) {
	return QueryResult{}, nil
}

func (connection *postgresRestoreConnection) Close(context.Context) error {
	connection.closeCalls++
	return nil
}

type postgresRestorePublisher struct{ events []string }

func (publisher *postgresRestorePublisher) PublishPostgres(_ state.ManagedPostgres, container containerengine.Container) error {
	publisher.events = append(publisher.events, "publish:"+container.ID)
	return nil
}

func (publisher *postgresRestorePublisher) WithdrawPostgres(resource state.ManagedPostgres) error {
	publisher.events = append(publisher.events, "withdraw:"+resource.ID)
	return nil
}

type recordingPostgresMaintenance struct {
	projectID string
	address   netip.Addr
	port      uint16
	released  bool
}

func (maintenance *recordingPostgresMaintenance) BlockDatabase(
	_ context.Context,
	projectID string,
	address netip.Addr,
	port uint16,
) (func() error, error) {
	maintenance.projectID = projectID
	maintenance.address = address
	maintenance.port = port
	return func() error {
		maintenance.released = true
		return nil
	}, nil
}

func TestPostgresRestoreReplaceImportsCandidateAndDeletesOldVolume(t *testing.T) {
	t.Parallel()
	fixture := newPostgresRestoreFixture(t, nil)
	dump := []byte("PGDMP-custom-format")
	if err := fixture.controller.RestoreReplace(context.Background(), "postgres-id", bytes.NewReader(dump), Actor{
		Kind: "system", ID: "disaster_restore",
	}); err != nil {
		t.Fatal(err)
	}

	if fixture.store.resource.VolumeID != "new-volume" || fixture.store.switchInput.Action != "postgres.restore" ||
		fixture.store.switchInput.AuditEventID != "audit-id" || fixture.store.switchInput.RequestCorrelationID != "correlation-id" ||
		fixture.store.switchInput.ActorKind != "system" || fixture.store.switchInput.ActorID != "disaster_restore" {
		t.Fatalf("volume switch = %+v, resource = %+v", fixture.store.switchInput, fixture.store.resource)
	}
	if !bytes.Equal(fixture.engine.execPayload, dump) {
		t.Fatalf("pg_restore stdin = %q", fixture.engine.execPayload)
	}
	wantCommand := []string{
		"pg_restore", "--exit-on-error", "--no-owner", "--no-acl",
		"--host=127.0.0.1", "--port=5432", "--dbname=application_db", "--username=application_owner",
	}
	if !reflect.DeepEqual(fixture.engine.execRequest.Command, wantCommand) ||
		fixture.engine.execRequest.Environment["PGPASSWORD"] != "owner-password" {
		t.Fatalf("pg_restore request = %+v", fixture.engine.execRequest)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "old-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old volume still exists: %v", err)
	}
	candidateID := "platformd-postgres-runtime-id-container"
	active, exists := fixture.controller.activeRuntime("postgres-id")
	if !exists || active.container.ID != candidateID || active.resource.VolumeID != "new-volume" {
		t.Fatalf("active runtime = %+v, %v", active, exists)
	}
	if _, exists := fixture.engine.containers["old-container"]; exists {
		t.Fatal("old container was not removed")
	}
	spec := fixture.engine.created[0]
	if spec.Name != "platformd-postgres-runtime-id" || spec.Labels["io.platformd.postgres-id"] != "postgres-id" ||
		!strings.HasSuffix(spec.LogPath, "/postgres/postgres-id/attempt-id.log") {
		t.Fatalf("candidate identity/profile = %+v", spec)
	}
	if !reflect.DeepEqual(fixture.publisher.events, []string{"withdraw:postgres-id", "publish:" + candidateID}) {
		t.Fatalf("publication events = %v", fixture.publisher.events)
	}
}

func TestPostgresRestoreReplaceRestartsOldRuntimeWhenPointerSwitchFails(t *testing.T) {
	t.Parallel()
	fixture := newPostgresRestoreFixture(t, errors.New("forced switch failure"))
	err := fixture.controller.RestoreReplace(context.Background(), "postgres-id", strings.NewReader("PGDMP-dump"), Actor{
		Kind: "token", ID: "token",
	})
	if err == nil || !strings.Contains(err.Error(), "forced switch failure") {
		t.Fatalf("restore error = %v", err)
	}
	if fixture.store.resource.VolumeID != "old-volume" {
		t.Fatalf("active volume = %q", fixture.store.resource.VolumeID)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "old-volume", "current-data")); err != nil {
		t.Fatalf("old volume was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "new-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed candidate volume still exists: %v", err)
	}
	old, exists := fixture.engine.containers["old-container"]
	if !exists || old.State != "running" {
		t.Fatalf("old container = %+v, %v", old, exists)
	}
	if _, exists := fixture.engine.containers["platformd-postgres-runtime-id-container"]; exists {
		t.Fatal("failed candidate container was not removed")
	}
	if !reflect.DeepEqual(fixture.publisher.events, []string{"withdraw:postgres-id", "publish:old-container"}) {
		t.Fatalf("rollback publication events = %v", fixture.publisher.events)
	}
}

func TestPostgresRestoreReplaceRejectsFailedImportBeforeDowntime(t *testing.T) {
	t.Parallel()
	fixture := newPostgresRestoreFixture(t, nil)
	fixture.engine.execCode = 1
	err := fixture.controller.RestoreReplace(context.Background(), "postgres-id", strings.NewReader("not-a-dump"), Actor{
		Kind: "access", ID: "user", Email: "user@example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "pg_restore exited with code 1") || strings.Contains(err.Error(), "%!w") {
		t.Fatalf("pg_restore error = %v", err)
	}
	if fixture.store.switchInput.ResourceID != "" || len(fixture.publisher.events) != 0 {
		t.Fatalf("failed import reached publication: switch=%+v events=%v", fixture.store.switchInput, fixture.publisher.events)
	}
	old, exists := fixture.engine.containers["old-container"]
	if !exists || old.State != "running" {
		t.Fatalf("old container = %+v, %v", old, exists)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "new-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed import volume still exists: %v", err)
	}
}

type postgresRestoreFixture struct {
	controller  *Controller
	store       *postgresRestoreStore
	engine      *postgresRestoreEngine
	publisher   *postgresRestorePublisher
	maintenance *recordingPostgresMaintenance
	volumeRoot  string
}

func newPostgresRestoreFixture(t *testing.T, switchErr error) postgresRestoreFixture {
	t.Helper()
	root := t.TempDir()
	volumeRoot := filepath.Join(root, "volumes")
	oldVolume, err := ensureVolume(volumeRoot, "project-id", "old-volume")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldVolume, "current-data"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	resource := state.ManagedPostgres{
		ID: "postgres-id", ProjectID: "project-id", ProjectName: "shop", Name: "database",
		ImageTag: "17", ImageDigest: restoreTestImageDigest, VolumeID: "old-volume",
		DatabaseName: "application_db", OwnerUsername: "application_owner",
	}
	store := &postgresRestoreStore{resource: resource, switchErr: switchErr}
	engine := &postgresRestoreEngine{
		image: containerengine.Image{ID: "image-id", Digest: restoreTestImageDigest},
		containers: map[string]containerengine.Container{
			"old-container": {
				ID: "old-container", State: "running",
				IPs: map[string][]string{"network": {"10.90.0.4"}},
			},
		},
	}
	publisher := &postgresRestorePublisher{}
	maintenance := &recordingPostgresMaintenance{}
	ids := []string{"new-volume", "runtime-id", "audit-id", "correlation-id", "attempt-id"}
	controller, err := NewController(ControllerConfig{
		Store: store, Engine: engine, Publisher: publisher, Growth: allowGrowthGate{}, Maintenance: maintenance, Admission: admission.New(),
		OwnerPassword:     func(state.ManagedPostgres) (string, error) { return "owner-password", nil },
		BootstrapPassword: func(state.ManagedPostgres) (string, error) { return "bootstrap-password", nil },
		Placement: func(state.ManagedPostgres) (Placement, error) {
			return Placement{
				NetworkName: "network", Gateway: netip.MustParseAddr("10.90.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/workload/postgres-id",
			}, nil
		},
		Dial: func(context.Context, string, string, string, string) (Connection, error) {
			return &postgresRestoreConnection{}, nil
		},
		VolumeRoot: volumeRoot, LogRoot: filepath.Join(root, "logs"),
		LogSizeBytes: 1 << 20, LogMaxFiles: 3, ReadyTimeout: time.Second, ProbePeriod: time.Millisecond,
		MaintenanceDrain: time.Nanosecond,
		Now:              func() time.Time { return time.UnixMilli(10) },
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
		resource: resource, container: engine.containers["old-container"], network: "network",
	})
	return postgresRestoreFixture{
		controller: controller, store: store, engine: engine, publisher: publisher, maintenance: maintenance,
		volumeRoot: volumeRoot,
	}
}
