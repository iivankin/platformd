package automation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/resourcemetrics"
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrUsageRange = errors.New("invalid usage range")
	ErrUsageKind  = errors.New("invalid usage kind")
)

var usageWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"1d":  24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

type UsageRepository interface {
	Project(context.Context, string) (state.ProjectSummary, error)
	Service(context.Context, string, string) (state.ServiceDesired, error)
	ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error)
	ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error)
}

type UsageReader interface {
	Read(cgroupstats.Kind, string) (resourcemetrics.Current, error)
	History(context.Context, cgroupstats.Kind, string, time.Duration) (resourcemetrics.History, error)
	ReadProject(string) (resourcemetrics.Current, error)
	ProjectHistory(context.Context, string, time.Duration) (resourcemetrics.History, error)
	ReadInstallation() (resourcemetrics.Current, error)
	InstallationHistory(context.Context, time.Duration) (resourcemetrics.History, error)
	HostHistory(context.Context, time.Duration) (resourcemetrics.History, error)
}

type UsageApplication struct {
	repository UsageRepository
	metrics    UsageReader
}

type ReadResourceUsageInput struct {
	ProjectID  string
	Kind       string
	ResourceID string
	Range      string
}

type ReadProjectUsageInput struct {
	ProjectID string
	Range     string
}

type ReadInstallationUsageInput struct {
	Range string
	Scope string
}

func NewUsageApplication(repository UsageRepository, metrics UsageReader) (*UsageApplication, error) {
	if repository == nil || metrics == nil {
		return nil, errors.New("usage automation dependencies are incomplete")
	}
	return &UsageApplication{repository: repository, metrics: metrics}, nil
}

func (application *UsageApplication) ReadResource(ctx context.Context, identity Identity, input ReadResourceUsageInput) (any, error) {
	if err := requireReadIdentity(identity); err != nil {
		return nil, err
	}
	if input.ProjectID == "" || input.ResourceID == "" {
		return nil, fmt.Errorf("%w: projectId and resourceId are required", ErrUsageKind)
	}
	kind, err := parseUsageKind(input.Kind)
	if err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, ErrProjectBoundary
	}
	if err := application.verifyResource(ctx, input.ProjectID, kind, input.ResourceID); err != nil {
		return nil, err
	}
	if input.Range == "" {
		current, err := application.metrics.Read(kind, input.ResourceID)
		if err != nil {
			return nil, err
		}
		return usageSnapshotFor(current), nil
	}
	window, err := parseUsageRange(input.Range)
	if err != nil {
		return nil, err
	}
	history, err := application.metrics.History(ctx, kind, input.ResourceID, window)
	if err != nil {
		return nil, err
	}
	return usageHistoryFor(history), nil
}

func (application *UsageApplication) ReadProject(ctx context.Context, identity Identity, input ReadProjectUsageInput) (any, error) {
	if err := requireReadIdentity(identity); err != nil {
		return nil, err
	}
	if input.ProjectID == "" {
		return nil, fmt.Errorf("%w: projectId is required", ErrUsageKind)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, ErrProjectBoundary
	}
	if _, err := application.repository.Project(ctx, input.ProjectID); err != nil {
		return nil, err
	}
	if input.Range == "" {
		current, err := application.metrics.ReadProject(input.ProjectID)
		if err != nil {
			return nil, err
		}
		return usageSnapshotFor(current), nil
	}
	window, err := parseUsageRange(input.Range)
	if err != nil {
		return nil, err
	}
	history, err := application.metrics.ProjectHistory(ctx, input.ProjectID, window)
	if err != nil {
		return nil, err
	}
	return usageHistoryFor(history), nil
}

func (application *UsageApplication) ReadInstallation(ctx context.Context, identity Identity, input ReadInstallationUsageInput) (any, error) {
	if !identity.IsAdmin() || identity.ProjectID != nil || identity.TokenID == "" {
		return nil, ErrUnboundAdminRequired
	}
	scope := input.Scope
	if scope == "" {
		scope = "installation"
	}
	switch scope {
	case "installation":
		if input.Range == "" {
			current, err := application.metrics.ReadInstallation()
			if err != nil {
				return nil, err
			}
			return usageSnapshotFor(current), nil
		}
		window, err := parseUsageRange(input.Range)
		if err != nil {
			return nil, err
		}
		history, err := application.metrics.InstallationHistory(ctx, window)
		if err != nil {
			return nil, err
		}
		return usageHistoryFor(history), nil
	case "host":
		if input.Range == "" {
			return nil, fmt.Errorf("%w: host scope requires range", ErrUsageRange)
		}
		window, err := parseUsageRange(input.Range)
		if err != nil {
			return nil, err
		}
		history, err := application.metrics.HostHistory(ctx, window)
		if err != nil {
			return nil, err
		}
		return usageHistoryFor(history), nil
	default:
		return nil, fmt.Errorf("%w: scope must be installation or host", ErrUsageKind)
	}
}

func (application *UsageApplication) verifyResource(ctx context.Context, projectID string, kind cgroupstats.Kind, resourceID string) error {
	switch kind {
	case cgroupstats.Service:
		_, err := application.repository.Service(ctx, projectID, resourceID)
		return err
	case cgroupstats.Postgres:
		_, err := application.repository.ManagedPostgresInProject(ctx, projectID, resourceID)
		return err
	case cgroupstats.Redis:
		_, err := application.repository.ManagedRedisInProject(ctx, projectID, resourceID)
		return err
	default:
		return ErrUsageKind
	}
}

func requireReadIdentity(identity Identity) error {
	if identity.TokenID == "" || identity.Role != "read" && identity.Role != "admin" {
		return ErrReadTokenRequired
	}
	return nil
}

func parseUsageKind(value string) (cgroupstats.Kind, error) {
	kind := cgroupstats.Kind(value)
	switch kind {
	case cgroupstats.Service, cgroupstats.Postgres, cgroupstats.Redis:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: kind must be service, postgres, or redis", ErrUsageKind)
	}
}

func parseUsageRange(value string) (time.Duration, error) {
	window, ok := usageWindows[value]
	if !ok {
		return 0, fmt.Errorf("%w: range must be 1h, 6h, 1d, 7d, or 30d", ErrUsageRange)
	}
	return window, nil
}
