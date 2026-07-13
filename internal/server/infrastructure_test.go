package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/server"
)

type pressureStub struct {
	snapshot diskpressure.Snapshot
	ready    bool
}

func (pressure pressureStub) Snapshot() (diskpressure.Snapshot, bool) {
	return pressure.snapshot, pressure.ready
}

type usageStub struct{}

func (usageStub) Read(kind cgroupstats.Kind, resourceID string) (cgroupstats.Sample, error) {
	if kind != cgroupstats.Service || resourceID != "api" {
		return cgroupstats.Sample{}, cgroupstats.ErrInvalidResource
	}
	return cgroupstats.Sample{
		ObservedAtMillis: 42, CPUUsageMicros: 123_456, MemoryBytes: 64 << 20,
		HostCPUCores: 8, HostMemoryBytes: 16 << 30, Running: true,
	}, nil
}

func TestInfrastructureShowsDerivedDiskPressureWithoutPersistentState(t *testing.T) {
	t.Parallel()

	direct := server.Handler(server.DefaultMeta("ready"), server.WithDiskPressure(pressureStub{
		ready: true,
		snapshot: diskpressure.Snapshot{
			Level: diskpressure.Critical, ReservePresent: false, CheckedAt: time.UnixMilli(42),
			Usage: diskpressure.Usage{TotalBytes: 100, AvailableBytes: 4, TotalInodes: 1000, AvailableInodes: 500, ByteBasisPoints: 9600, InodeBasisPoints: 5000},
		},
	}))
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/infrastructure/disk-pressure", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("direct disk pressure = %d/%s", response.Code, response.Body)
	}
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/infrastructure/disk-pressure", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"level":"critical"`) || !strings.Contains(response.Body.String(), `"byteBasisPoints":9600`) || !strings.Contains(response.Body.String(), `"reservePresent":false`) {
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
		!strings.Contains(response.Body.String(), `"memoryBytes":67108864`) || !strings.Contains(response.Body.String(), `"running":true`) {
		t.Fatalf("resource usage = %d/%s", response.Code, response.Body)
	}
}
