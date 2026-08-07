package automation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/resourcemetrics"
	"github.com/iivankin/platformd/internal/state"
)

type usageRepositoryStub struct {
	projectCalls  int
	serviceCalls  int
	postgresCalls int
	redisCalls    int
}

func (repository *usageRepositoryStub) Project(context.Context, string) (state.ProjectSummary, error) {
	repository.projectCalls++
	return state.ProjectSummary{ID: "project", Name: "shop"}, nil
}

func (repository *usageRepositoryStub) Service(context.Context, string, string) (state.ServiceDesired, error) {
	repository.serviceCalls++
	return state.ServiceDesired{ID: "service", ProjectID: "project"}, nil
}

func (repository *usageRepositoryStub) ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error) {
	repository.postgresCalls++
	return state.ManagedPostgres{ID: "postgres", ProjectID: "project"}, nil
}

func (repository *usageRepositoryStub) ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error) {
	repository.redisCalls++
	return state.ManagedRedis{ID: "redis", ProjectID: "project"}, nil
}

type usageMetricsStub struct {
	readCalls    int
	historyCalls int
	projectCalls int
	kind         cgroupstats.Kind
	resourceID   string
	projectID    string
	window       time.Duration
}

func (metrics *usageMetricsStub) Read(kind cgroupstats.Kind, resourceID string) (resourcemetrics.Current, error) {
	metrics.readCalls++
	metrics.kind = kind
	metrics.resourceID = resourceID
	cpu := int64(250)
	return resourcemetrics.Current{
		Sample:        cgroupstats.Sample{ObservedAtMillis: 10, MemoryBytes: 1024, Running: true},
		CPUMillicores: &cpu,
	}, nil
}

func (metrics *usageMetricsStub) History(_ context.Context, kind cgroupstats.Kind, resourceID string, window time.Duration) (resourcemetrics.History, error) {
	metrics.historyCalls++
	metrics.kind = kind
	metrics.resourceID = resourceID
	metrics.window = window
	return resourcemetrics.History{
		From: 1, To: 2, StepMillis: 60000,
		Points: []resourcemetrics.Point{{ObservedAt: 1, MemoryBytes: 2048, Running: true}},
	}, nil
}

func (metrics *usageMetricsStub) ReadProject(projectID string) (resourcemetrics.Current, error) {
	metrics.projectCalls++
	metrics.projectID = projectID
	return resourcemetrics.Current{
		Sample:           cgroupstats.Sample{ObservedAtMillis: 11, MemoryBytes: 4096, Running: true},
		RunningResources: 2, TotalResources: 3,
	}, nil
}

func (metrics *usageMetricsStub) ProjectHistory(_ context.Context, projectID string, window time.Duration) (resourcemetrics.History, error) {
	metrics.projectCalls++
	metrics.projectID = projectID
	metrics.window = window
	return resourcemetrics.History{
		From: 3, To: 4, StepMillis: 300000,
		Series: []resourcemetrics.HistorySeries{{
			ID: "service", Kind: "service", Name: "api",
			Points: []resourcemetrics.Point{{ObservedAt: 3, MemoryBytes: 512, Running: true}},
		}},
	}, nil
}

func (metrics *usageMetricsStub) ReadInstallation() (resourcemetrics.Current, error) {
	return resourcemetrics.Current{
		Sample:           cgroupstats.Sample{ObservedAtMillis: 12, MemoryBytes: 8192, Running: true},
		RunningResources: 4, TotalResources: 5,
	}, nil
}

func (metrics *usageMetricsStub) InstallationHistory(_ context.Context, window time.Duration) (resourcemetrics.History, error) {
	metrics.window = window
	return resourcemetrics.History{From: 5, To: 6, StepMillis: 60000}, nil
}

func (metrics *usageMetricsStub) HostHistory(_ context.Context, window time.Duration) (resourcemetrics.History, error) {
	metrics.window = window
	return resourcemetrics.History{From: 7, To: 8, StepMillis: 60000}, nil
}

func TestUsageApplicationAuthorizesBeforeLookup(t *testing.T) {
	repository := &usageRepositoryStub{}
	metrics := &usageMetricsStub{}
	application, err := NewUsageApplication(repository, metrics)
	if err != nil {
		t.Fatal(err)
	}
	input := ReadResourceUsageInput{ProjectID: "project", Kind: "service", ResourceID: "service"}
	if _, err := application.ReadResource(context.Background(), Identity{TokenID: "token", Role: "unknown"}, input); !errors.Is(err, ErrReadTokenRequired) {
		t.Fatalf("role error = %v", err)
	}
	bound := "other"
	if _, err := application.ReadResource(context.Background(), Identity{TokenID: "token", Role: "read", ProjectID: &bound}, input); !errors.Is(err, ErrProjectBoundary) {
		t.Fatalf("boundary error = %v", err)
	}
	if repository.serviceCalls != 0 || metrics.readCalls != 0 {
		t.Fatalf("dependencies called before authorization: services=%d metrics=%d", repository.serviceCalls, metrics.readCalls)
	}
	snapshot, err := application.ReadResource(context.Background(), Identity{TokenID: "token", Role: "read"}, input)
	current, ok := snapshot.(UsageSnapshot)
	if err != nil || !ok || current.MemoryBytes != 1024 || repository.serviceCalls != 1 || metrics.readCalls != 1 || metrics.kind != cgroupstats.Service {
		t.Fatalf("read = %+v ok=%v services=%d metrics=%d kind=%s err=%v", snapshot, ok, repository.serviceCalls, metrics.readCalls, metrics.kind, err)
	}
}

func TestUsageApplicationReadsHistoryAndProject(t *testing.T) {
	repository := &usageRepositoryStub{}
	metrics := &usageMetricsStub{}
	application, err := NewUsageApplication(repository, metrics)
	if err != nil {
		t.Fatal(err)
	}
	history, err := application.ReadResource(context.Background(), Identity{TokenID: "token", Role: "admin"}, ReadResourceUsageInput{
		ProjectID: "project", Kind: "redis", ResourceID: "redis", Range: "6h",
	})
	result, ok := history.(UsageHistory)
	if err != nil || !ok || len(result.Points) != 1 || metrics.window != 6*time.Hour || metrics.kind != cgroupstats.Redis || repository.redisCalls != 1 {
		t.Fatalf("history = %+v ok=%v window=%s kind=%s redisCalls=%d err=%v", history, ok, metrics.window, metrics.kind, repository.redisCalls, err)
	}
	project, err := application.ReadProject(context.Background(), Identity{TokenID: "token", Role: "read"}, ReadProjectUsageInput{
		ProjectID: "project", Range: "1d",
	})
	projectHistory, ok := project.(UsageHistory)
	if err != nil || !ok || len(projectHistory.Series) != 1 || metrics.window != 24*time.Hour || repository.projectCalls != 1 {
		t.Fatalf("project = %+v ok=%v window=%s projectCalls=%d err=%v", project, ok, metrics.window, repository.projectCalls, err)
	}
	if _, err := application.ReadResource(context.Background(), Identity{TokenID: "token", Role: "read"}, ReadResourceUsageInput{
		ProjectID: "project", Kind: "bucket", ResourceID: "assets",
	}); !errors.Is(err, ErrUsageKind) {
		t.Fatalf("kind error = %v", err)
	}
	if _, err := application.ReadProject(context.Background(), Identity{TokenID: "token", Role: "read"}, ReadProjectUsageInput{
		ProjectID: "project", Range: "2h",
	}); !errors.Is(err, ErrUsageRange) {
		t.Fatalf("range error = %v", err)
	}
}
