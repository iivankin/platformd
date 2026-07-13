package managedpostgres

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

type backupEngineStub struct {
	request  containerengine.ExecRequest
	payload  []byte
	stderr   string
	exitCode int
	execErr  error
	done     chan struct{}
}

func (*backupEngineStub) Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error) {
	return containerengine.Image{}, errors.New("unexpected Pull")
}
func (*backupEngineStub) InspectImage(context.Context, string) (containerengine.Image, error) {
	return containerengine.Image{}, errors.New("unexpected InspectImage")
}
func (*backupEngineStub) CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error) {
	return containerengine.Container{}, errors.New("unexpected CreateContainer")
}
func (*backupEngineStub) StartContainer(context.Context, string) error {
	return errors.New("unexpected StartContainer")
}
func (*backupEngineStub) StopContainer(string, uint) error {
	return errors.New("unexpected StopContainer")
}
func (*backupEngineStub) RemoveContainer(context.Context, string, bool) error {
	return errors.New("unexpected RemoveContainer")
}
func (*backupEngineStub) InspectContainer(string) (containerengine.Container, error) {
	return containerengine.Container{ID: "container", State: "running"}, nil
}
func (engine *backupEngineStub) ExecContainer(_ context.Context, _ string, request containerengine.ExecRequest) (int, error) {
	engine.request = request
	if request.Stdout != nil {
		_, _ = request.Stdout.Write(engine.payload)
	}
	if request.Stderr != nil {
		_, _ = io.WriteString(request.Stderr, engine.stderr)
	}
	close(engine.done)
	return engine.exitCode, engine.execErr
}

func TestOpenBackupDumpStreamsOwnerCustomFormatAndReleasesLocks(t *testing.T) {
	t.Parallel()
	engine := &backupEngineStub{payload: []byte("PGDMP\x01custom"), done: make(chan struct{})}
	gate := admission.New()
	resource := state.ManagedPostgres{
		ID: "postgres-id", DatabaseName: "application_db", OwnerUsername: "application_owner",
	}
	controller := &Controller{
		engine: engine, admission: gate,
		ownerPassword: func(state.ManagedPostgres) (string, error) { return "owner-password", nil },
		locks:         make(map[string]*sync.Mutex),
		active: map[string]activeRuntime{
			resource.ID: {resource: resource, container: containerengine.Container{ID: "container", State: "running"}},
		},
	}
	reader, err := controller.OpenBackupDump(context.Background(), resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(payload, engine.payload) {
		t.Fatalf("pg_dump stream = %q, read=%v close=%v", payload, readErr, closeErr)
	}
	<-engine.done
	wantCommand := []string{
		"pg_dump", "--format=custom", "--no-owner", "--no-acl",
		"--dbname=application_db", "--username=application_owner",
	}
	if !reflect.DeepEqual(engine.request.Command, wantCommand) || engine.request.Environment["PGPASSWORD"] != "owner-password" {
		t.Fatalf("pg_dump request = %+v", engine.request)
	}
	if snapshot, updating := gate.Snapshot(); updating || snapshot.Total != 0 {
		t.Fatalf("backup admission lease survived export: %+v updating=%t", snapshot, updating)
	}
	lock := controller.resourceLock(resource.ID)
	if !lock.TryLock() {
		t.Fatal("resource lock survived completed pg_dump")
	}
	lock.Unlock()
}

func TestOpenBackupDumpPropagatesNonzeroExitAndBoundedStderr(t *testing.T) {
	t.Parallel()
	engine := &backupEngineStub{
		stderr:   string(bytes.Repeat([]byte("x"), maximumBackupDiagnosticBytes+1024)),
		exitCode: 2, done: make(chan struct{}),
	}
	resource := state.ManagedPostgres{ID: "postgres-id", DatabaseName: "db", OwnerUsername: "owner"}
	controller := &Controller{
		engine: engine, admission: admission.New(),
		ownerPassword: func(state.ManagedPostgres) (string, error) { return "password", nil },
		locks:         make(map[string]*sync.Mutex),
		active: map[string]activeRuntime{
			resource.ID: {resource: resource, container: containerengine.Container{ID: "container", State: "running"}},
		},
	}
	reader, err := controller.OpenBackupDump(context.Background(), resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(reader)
	_ = reader.Close()
	if err == nil || len(err.Error()) > maximumBackupDiagnosticBytes+256 {
		t.Fatalf("pg_dump failure = %v (length %d)", err, len(err.Error()))
	}
}
