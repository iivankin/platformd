package automation

import (
	"context"
	"errors"
	"fmt"
	"time"

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

type ManagedLogReader interface {
	ResourceLogs(ctx context.Context, projectID string, query containerlogs.ResourceQuery) (containerlogs.Window, error)
}

type LogApplication struct {
	services ServiceLookup
	reader   ContainerLogReader
	managed  ManagedLogReader
}

type ReadServiceLogsInput struct {
	ProjectID    string
	ServiceID    string
	DeploymentID string
	Contains     string
	Cursor       string
	FieldFilters []containerlogs.FieldFilter
	SeverityText string
	TraceID      string
	SpanID       string
	From         time.Time
	To           time.Time
	Limit        int
	Ascending    bool
}

type ReadResourceLogsInput struct {
	ProjectID    string
	Kind         string
	ResourceID   string
	DeploymentID string
	Contains     string
	Cursor       string
	Limit        int
}

func NewLogApplication(services ServiceLookup, reader ContainerLogReader, managed ManagedLogReader) (*LogApplication, error) {
	if services == nil || reader == nil {
		return nil, errors.New("log automation dependencies are incomplete")
	}
	return &LogApplication{services: services, reader: reader, managed: managed}, nil
}

func (application *LogApplication) SupportsManagedResources() bool {
	return application.managed != nil
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
		Contains: input.Contains, Cursor: input.Cursor, FieldFilters: input.FieldFilters, SeverityText: input.SeverityText,
		TraceID: input.TraceID, SpanID: input.SpanID, From: input.From, To: input.To,
		Limit: input.Limit, Ascending: input.Ascending,
	})
}

func (application *LogApplication) ReadResource(ctx context.Context, identity Identity, input ReadResourceLogsInput) (containerlogs.Window, error) {
	if identity.TokenID == "" || identity.Role != "read" && identity.Role != "admin" {
		return containerlogs.Window{}, ErrReadTokenRequired
	}
	if application.managed == nil {
		return containerlogs.Window{}, errors.New("managed resource logs are unavailable")
	}
	if input.ProjectID == "" || input.ResourceID == "" {
		return containerlogs.Window{}, errors.New("projectId and resourceId are required")
	}
	switch input.Kind {
	case "postgres", "redis":
	default:
		return containerlogs.Window{}, fmt.Errorf("%w: kind must be postgres or redis", ErrInvalidInput)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return containerlogs.Window{}, ErrProjectBoundary
	}
	return application.managed.ResourceLogs(ctx, input.ProjectID, containerlogs.ResourceQuery{
		Kind: input.Kind, ResourceID: input.ResourceID,
		Query: containerlogs.Query{
			DeploymentID: input.DeploymentID, Contains: input.Contains,
			Cursor: input.Cursor, Limit: input.Limit,
		},
	})
}
