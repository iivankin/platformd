package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/diskusage"
	"github.com/iivankin/platformd/internal/journallogs"
	"github.com/iivankin/platformd/internal/resourcemetrics"
	"github.com/iivankin/platformd/internal/server"
)

type pressureStub struct {
	snapshot diskpressure.Snapshot
	ready    bool
}

func (pressure pressureStub) Snapshot() (diskpressure.Snapshot, bool) {
	return pressure.snapshot, pressure.ready
}

type capacityStub struct {
	pressureStub
}

func (capacityStub) Components(context.Context) (diskusage.Snapshot, error) {
	return diskusage.Snapshot{
		CheckedAt:  time.UnixMilli(40),
		Components: []diskusage.Component{{ID: "volumes", Bytes: 24}},
	}, nil
}

type usageStub struct{}

type journalStub struct {
	calls int
	query journallogs.Query
}

func (logs *journalStub) Read(_ context.Context, query journallogs.Query) (journallogs.Window, error) {
	logs.calls++
	logs.query = query
	return journallogs.Window{Records: []journallogs.Record{{
		Timestamp: time.Unix(1, 0).UTC(), Priority: 6, Message: "platform ready",
		Identifier: "platformd", PID: "42", Cursor: "cursor",
	}}, Truncated: true}, nil
}

func (usageStub) Read(kind cgroupstats.Kind, resourceID string) (resourcemetrics.Current, error) {
	if kind != cgroupstats.Service || resourceID != "api" {
		return resourcemetrics.Current{}, cgroupstats.ErrInvalidResource
	}
	return resourcemetrics.Current{
		Sample: cgroupstats.Sample{
			ObservedAtMillis: 42, CPUUsageMicros: 123_456, MemoryBytes: 64 << 20,
			HostCPUCores: 8, HostMemoryBytes: 16 << 30, Running: true,
		},
		NetworkRXBytes: 100, NetworkTXBytes: 200, NetworkAvailable: true,
	}, nil
}

func (usageStub) History(_ context.Context, kind cgroupstats.Kind, resourceID string, window time.Duration) (resourcemetrics.History, error) {
	if kind != cgroupstats.Service || resourceID != "api" || window != 6*time.Hour {
		return resourcemetrics.History{}, cgroupstats.ErrInvalidResource
	}
	cpu, ingress, egress := int64(12), int64(34), int64(56)
	return resourcemetrics.History{
		From: 1, To: 2, StepMillis: 300_000,
		Points: []resourcemetrics.Point{{
			ObservedAt: 2, CPUMillicores: &cpu, MemoryBytes: 78,
			NetworkIngressBytesPerSecond: &ingress, NetworkEgressBytesPerSecond: &egress,
			Running: true,
		}},
	}, nil
}

func TestInfrastructureShowsDerivedDiskPressureWithoutPersistentState(t *testing.T) {
	t.Parallel()

	direct := server.Handler(server.DefaultMeta("ready"), server.WithDiskPressure(capacityStub{pressureStub: pressureStub{
		ready: true,
		snapshot: diskpressure.Snapshot{
			Level: diskpressure.Critical, ReservePresent: false, CheckedAt: time.UnixMilli(42),
			Usage: diskpressure.Usage{TotalBytes: 100, AvailableBytes: 4, TotalInodes: 1000, AvailableInodes: 500, ByteBasisPoints: 9600, InodeBasisPoints: 5000},
		},
	}}))
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/infrastructure/disk-pressure", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("direct disk pressure = %d/%s", response.Code, response.Body)
	}
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/infrastructure/disk-pressure", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"level":"critical"`) || !strings.Contains(response.Body.String(), `"byteBasisPoints":9600`) || !strings.Contains(response.Body.String(), `"reservePresent":false`) || !strings.Contains(response.Body.String(), `"id":"volumes","bytes":24`) {
		t.Fatalf("disk pressure = %d/%s", response.Code, response.Body)
	}
}

func TestInfrastructureReportsStatelessResourceCgroupUsage(t *testing.T) {
	t.Parallel()
	direct := server.Handler(server.DefaultMeta("ready"), server.WithResourceUsage(usageStub{}))
	path := "/api/v1/infrastructure/resources/service/api/usage"
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("direct resource usage = %d/%s", response.Code, response.Body)
	}
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, path, ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cpuUsageMicros":123456`) ||
		!strings.Contains(response.Body.String(), `"memoryBytes":67108864`) ||
		!strings.Contains(response.Body.String(), `"networkRxBytes":100`) ||
		!strings.Contains(response.Body.String(), `"networkAvailable":true`) ||
		!strings.Contains(response.Body.String(), `"running":true`) {
		t.Fatalf("resource usage = %d/%s", response.Code, response.Body)
	}
}

func TestInfrastructureReportsPersistedResourceUsageHistory(t *testing.T) {
	t.Parallel()
	direct := server.Handler(server.DefaultMeta("ready"), server.WithResourceUsage(usageStub{}))
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	path := "/api/v1/infrastructure/resources/service/api/usage/history?range=6h"
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, path, ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stepMillis":300000`) ||
		!strings.Contains(response.Body.String(), `"cpuMillicores":12`) ||
		!strings.Contains(response.Body.String(), `"networkIngressBytesPerSecond":34`) {
		t.Fatalf("resource usage history = %d/%s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/infrastructure/resources/service/api/usage/history?range=2h", ""))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid resource usage range = %d/%s", response.Code, response.Body)
	}
}

func TestInfrastructureReadsBoundedPlatformJournalOnlyWithAccess(t *testing.T) {
	t.Parallel()
	logs := &journalStub{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithInfrastructureLogs(logs))
	path := "/api/v1/infrastructure/logs?limit=25"
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusForbidden || logs.calls != 0 {
		t.Fatalf("direct journal = %d/%s calls=%d", response.Code, response.Body, logs.calls)
	}
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, path, ""))
	if response.Code != http.StatusOK || logs.calls != 1 || logs.query.Limit != 25 ||
		!strings.Contains(response.Body.String(), `"message":"platform ready"`) || !strings.Contains(response.Body.String(), `"truncated":true`) {
		t.Fatalf("journal = %d/%s calls=%d query=%+v", response.Code, response.Body, logs.calls, logs.query)
	}
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/infrastructure/logs?limit=2001", ""))
	if response.Code != http.StatusBadRequest || logs.calls != 1 {
		t.Fatalf("invalid journal query = %d/%s calls=%d", response.Code, response.Body, logs.calls)
	}
}
