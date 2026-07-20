package containerports

import (
	"context"
	"errors"

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
	ContainerListeningPorts(string) ([]containerengine.ListeningPort, error)
}

type Application struct {
	resources ResourceRepository
	runtime   Runtime
	engine    Engine
}

func New(resources ResourceRepository, runtime Runtime, engine Engine) (*Application, error) {
	if resources == nil || runtime == nil || engine == nil {
		return nil, errors.New("container port dependencies are incomplete")
	}
	return &Application{resources: resources, runtime: runtime, engine: engine}, nil
}

func (application *Application) List(
	ctx context.Context, projectID, resourceKind, resourceID string,
) ([]containerengine.ListeningPort, error) {
	if projectID == "" || resourceID == "" {
		return nil, errors.New("container port resource identity is incomplete")
	}
	if err := application.resources.Resource(ctx, projectID, resourceKind, resourceID); err != nil {
		return nil, err
	}
	target, active, err := application.runtime.ResourceContainer(resourceKind, resourceID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrResourceNotRunning
	}
	return application.engine.ContainerListeningPorts(target.ID)
}
