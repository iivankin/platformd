package containerfiles

import (
	"context"
	"errors"
	"io"

	"github.com/iivankin/platformd/internal/containerengine"
)

var ErrResourceNotRunning = errors.New("resource has no running container")

type ResourceRepository interface {
	Resource(context.Context, string, string, string) error
}

type Runtime interface {
	ResourceContainer(string, string) (containerengine.Container, bool, error)
}

type Engine interface {
	ListContainerFiles(context.Context, string, string) ([]containerengine.ContainerFileEntry, error)
	OpenContainerFile(context.Context, string, string) (io.ReadCloser, containerengine.ContainerFileEntry, error)
	WriteContainerFile(context.Context, string, string, io.Reader, int64) error
}

type Application struct {
	resources ResourceRepository
	runtime   Runtime
	engine    Engine
}

func New(resources ResourceRepository, runtime Runtime, engine Engine) (*Application, error) {
	if resources == nil || runtime == nil || engine == nil {
		return nil, errors.New("container file dependencies are incomplete")
	}
	return &Application{resources: resources, runtime: runtime, engine: engine}, nil
}

func (application *Application) List(
	ctx context.Context, projectID, resourceKind, resourceID, rootPath string,
) ([]containerengine.ContainerFileEntry, error) {
	containerID, err := application.containerID(ctx, projectID, resourceKind, resourceID)
	if err != nil {
		return nil, err
	}
	return application.engine.ListContainerFiles(ctx, containerID, rootPath)
}

func (application *Application) Open(
	ctx context.Context, projectID, resourceKind, resourceID, filePath string,
) (io.ReadCloser, containerengine.ContainerFileEntry, error) {
	containerID, err := application.containerID(ctx, projectID, resourceKind, resourceID)
	if err != nil {
		return nil, containerengine.ContainerFileEntry{}, err
	}
	return application.engine.OpenContainerFile(ctx, containerID, filePath)
}

func (application *Application) Write(
	ctx context.Context, projectID, resourceKind, resourceID, filePath string, source io.Reader, sizeBytes int64,
) error {
	containerID, err := application.containerID(ctx, projectID, resourceKind, resourceID)
	if err != nil {
		return err
	}
	return application.engine.WriteContainerFile(ctx, containerID, filePath, source, sizeBytes)
}

func (application *Application) containerID(
	ctx context.Context, projectID, resourceKind, resourceID string,
) (string, error) {
	if projectID == "" || resourceID == "" {
		return "", errors.New("container file resource identity is incomplete")
	}
	if err := application.resources.Resource(ctx, projectID, resourceKind, resourceID); err != nil {
		return "", err
	}
	target, active, err := application.runtime.ResourceContainer(resourceKind, resourceID)
	if err != nil {
		return "", err
	}
	if !active {
		return "", ErrResourceNotRunning
	}
	return target.ID, nil
}
