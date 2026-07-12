package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

var ErrReadTokenRequired = errors.New("read or admin token is required")

type ServiceLookup interface {
	Service(context.Context, string, string) (state.ServiceDesired, error)
}

type ContainerLogReader interface {
	Read(context.Context, containerlogs.Query) (containerlogs.Window, error)
}

type LogApplication struct {
	services ServiceLookup
	reader   ContainerLogReader
}

type ReadServiceLogsInput struct {
	ProjectID    string
	ServiceID    string
	DeploymentID string
	Contains     string
	Limit        int
}

func NewLogApplication(services ServiceLookup, reader ContainerLogReader) (*LogApplication, error) {
	if services == nil || reader == nil {
		return nil, errors.New("log automation dependencies are incomplete")
	}
	return &LogApplication{services: services, reader: reader}, nil
}

func (application *LogApplication) ReadService(ctx context.Context, identity Identity, input ReadServiceLogsInput) (containerlogs.Window, error) {
	if identity.TokenID == "" || identity.Role != "read" && identity.Role != "admin" {
		return containerlogs.Window{}, ErrReadTokenRequired
	}
	if input.ProjectID == "" || input.ServiceID == "" {
		return containerlogs.Window{}, errors.New("projectId and serviceId are required")
	}
	if !identity.AllowsProject(input.ProjectID) {
		return containerlogs.Window{}, ErrProjectBoundary
	}
	if _, err := application.services.Service(ctx, input.ProjectID, input.ServiceID); err != nil {
		return containerlogs.Window{}, err
	}
	return application.reader.Read(ctx, containerlogs.Query{
		ServiceID: input.ServiceID, DeploymentID: input.DeploymentID,
		Contains: input.Contains, Limit: input.Limit,
	})
}
