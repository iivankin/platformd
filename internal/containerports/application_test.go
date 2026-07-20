package containerports_test

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/containerports"
)

type resourceRepository struct{ calls int }

func (repository *resourceRepository) Resource(_ context.Context, projectID, kind, resourceID string) error {
	repository.calls++
	if projectID != "project" || kind != "service" || resourceID != "api" {
		return io.ErrUnexpectedEOF
	}
	return nil
}

type runtime struct{ active bool }

func (current runtime) ResourceContainer(kind, resourceID string) (containerengine.Container, bool, error) {
	if kind != "service" || resourceID != "api" {
		return containerengine.Container{}, false, io.ErrUnexpectedEOF
	}
	return containerengine.Container{ID: "container", State: "running"}, current.active, nil
}

type engine struct{ containerID string }

func (current *engine) ContainerListeningPorts(containerID string) ([]containerengine.ListeningPort, error) {
	current.containerID = containerID
	return []containerengine.ListeningPort{{Port: 8080, Protocol: "tcp"}}, nil
}

func TestApplicationBindsPortProbeToValidatedLiveResource(t *testing.T) {
	repository := &resourceRepository{}
	probe := &engine{}
	application, err := containerports.New(repository, runtime{active: true}, probe)
	if err != nil {
		t.Fatal(err)
	}
	ports, err := application.List(context.Background(), "project", "service", "api")
	if err != nil {
		t.Fatal(err)
	}
	want := []containerengine.ListeningPort{{Port: 8080, Protocol: "tcp"}}
	if repository.calls != 1 || probe.containerID != "container" || !reflect.DeepEqual(ports, want) {
		t.Fatalf("resource calls/container/ports = %d/%q/%#v", repository.calls, probe.containerID, ports)
	}
}

func TestApplicationRejectsStoppedResourceBeforePortProbe(t *testing.T) {
	probe := &engine{}
	application, err := containerports.New(&resourceRepository{}, runtime{}, probe)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.List(context.Background(), "project", "service", "api")
	if err != containerports.ErrResourceNotRunning || probe.containerID != "" {
		t.Fatalf("error/container = %v/%q, want ErrResourceNotRunning/no probe", err, probe.containerID)
	}
}
