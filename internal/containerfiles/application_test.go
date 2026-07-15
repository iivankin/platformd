package containerfiles_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/containerfiles"
)

type resourceRepository struct{ calls int }

func (repository *resourceRepository) Resource(_ context.Context, projectID, kind, resourceID string) error {
	repository.calls++
	if projectID != "project" || kind != "redis" || resourceID != "cache" {
		return io.ErrUnexpectedEOF
	}
	return nil
}

type runtime struct{ active bool }

func (runtime runtime) ResourceContainer(kind, resourceID string) (containerengine.Container, bool, error) {
	if kind != "redis" || resourceID != "cache" {
		return containerengine.Container{}, false, io.ErrUnexpectedEOF
	}
	return containerengine.Container{ID: "container", State: "running"}, runtime.active, nil
}

type engine struct {
	listedPath  string
	writtenPath string
	written     string
}

func (engine *engine) ListContainerFiles(_ context.Context, containerID, rootPath string) ([]containerengine.ContainerFileEntry, error) {
	if containerID != "container" {
		return nil, io.ErrUnexpectedEOF
	}
	engine.listedPath = rootPath
	return []containerengine.ContainerFileEntry{{Path: "/data/redis.conf"}}, nil
}

func (*engine) OpenContainerFile(context.Context, string, string) (io.ReadCloser, containerengine.ContainerFileEntry, error) {
	return nil, containerengine.ContainerFileEntry{}, io.ErrUnexpectedEOF
}

func (engine *engine) WriteContainerFile(_ context.Context, containerID, filePath string, source io.Reader, sizeBytes int64) error {
	if containerID != "container" || sizeBytes != 7 {
		return io.ErrUnexpectedEOF
	}
	payload, err := io.ReadAll(source)
	if err != nil {
		return err
	}
	engine.writtenPath = filePath
	engine.written = string(payload)
	return nil
}

func TestApplicationBindsFileOperationsToValidatedLiveResource(t *testing.T) {
	repository := &resourceRepository{}
	files := &engine{}
	application, err := containerfiles.New(repository, runtime{active: true}, files)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := application.List(context.Background(), "project", "redis", "cache", "/data")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || files.listedPath != "/data" {
		t.Fatalf("entries/path = %+v/%q", entries, files.listedPath)
	}
	if err := application.Write(
		context.Background(), "project", "redis", "cache", "/data/new", strings.NewReader("payload"), 7,
	); err != nil {
		t.Fatal(err)
	}
	if repository.calls != 2 || files.writtenPath != "/data/new" || files.written != "payload" {
		t.Fatalf("resource calls/write = %d/%q/%q", repository.calls, files.writtenPath, files.written)
	}
}

func TestApplicationRejectsStoppedResourceBeforeFilesystemAccess(t *testing.T) {
	application, err := containerfiles.New(&resourceRepository{}, runtime{}, &engine{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.List(context.Background(), "project", "redis", "cache", "/data")
	if err != containerfiles.ErrResourceNotRunning {
		t.Fatalf("error = %v, want ErrResourceNotRunning", err)
	}
}
