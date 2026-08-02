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
	"github.com/iivankin/platformd/internal/containerengine"
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

type pendingCapacityStub struct {
	pressureStub
}

type imageGarbageCollectorStub struct {
	calls int
}

func (collector *imageGarbageCollectorStub) ForceGarbageCollect(context.Context) (containerengine.ImageGarbageCollectResult, error) {
	collector.calls++
	return containerengine.ImageGarbageCollectResult{
		FinalImagesRemoved: 2, BuildCacheImagesRemoved: 3, OrphanLayersRemoved: 1,
		RemovedBytes: 42, Skipped: 4,
	}, nil
}

func (capacityStub) Components(context.Context) (diskusage.Snapshot, error) {
	return diskusage.Snapshot{
		CheckedAt:  time.UnixMilli(40),
		Components: []diskusage.Component{{ID: "volumes", Bytes: 24}},
	}, nil
}

func (pendingCapacityStub) Components(context.Context) (diskusage.Snapshot, error) {
	return diskusage.Snapshot{Components: []diskusage.Component{{ID: "volumes"}}}, nil
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
	}}, NextCursor: "next-cursor"}, nil
}

func (usageStub) Read(kind cgroupstats.Kind, resourceID string) (resourcemetrics.Current, error) {
	if kind != cgroupstats.Service || resourceID != "api" {
		return resourcemetrics.Current{}, cgroupstats.ErrInvalidResource
	}
	cpu, cpuPeak, ingress, ingressPeak, egress, egressPeak := int64(42), int64(84), int64(100), int64(300), int64(50), int64(150)
	rps, rpsPeak, p95 := 12.5, 40.0, 90.0
	return resourcemetrics.Current{
		Sample: cgroupstats.Sample{
			ObservedAtMillis: 42, CPUUsageMicros: 123_456, MemoryBytes: 64 << 20,
			HostCPUCores: 8, HostMemoryBytes: 16 << 30, Running: true,
		},
		NetworkRXBytes: 100, NetworkTXBytes: 200, NetworkAvailable: true,
		CPUMillicores: &cpu, CPUPeakMillicores: &cpuPeak, MemoryPeakBytes: 70 << 20,
		NetworkIngressBytesPerSecond: &ingress, NetworkIngressPeakBytesPerSecond: &ingressPeak,
		NetworkEgressBytesPerSecond: &egress, NetworkEgressPeakBytesPerSecond: &egressPeak,
		RunningResources: 1, TotalResources: 1,
		Proxy: &resourcemetrics.ProxyMetrics{
			HTTP: resourcemetrics.HTTPMetrics{RequestsPerSecond: &rps, RequestsPeakPerSecond: &rpsPeak, RequestsTotal: 100, LatencyP95Millis: &p95},
		},
	}, nil
}

func (usageStub) History(_ context.Context, kind cgroupstats.Kind, resourceID string, window time.Duration) (resourcemetrics.History, error) {
	if kind != cgroupstats.Service || resourceID != "api" || window != 6*time.Hour {
		return resourcemetrics.History{}, cgroupstats.ErrInvalidResource
	}
	cpu, cpuPeak, ingress, ingressPeak, egress, egressPeak := int64(12), int64(90), int64(34), int64(120), int64(56), int64(180)
	rps, rpsPeak := 8.5, 30.0
	return resourcemetrics.History{
		From: 1, To: 2, StepMillis: 300_000,
		Points: []resourcemetrics.Point{{
			ObservedAt: 2, DurationMillis: 60_000, CPUMillicores: &cpu, CPUPeakMillicores: &cpuPeak,
			MemoryBytes: 78, MemoryPeakBytes: 100,
			NetworkIngressBytesPerSecond: &ingress, NetworkIngressPeakBytesPerSecond: &ingressPeak,
			NetworkEgressBytesPerSecond: &egress, NetworkEgressPeakBytesPerSecond: &egressPeak,
			Running: true, Proxy: &resourcemetrics.ProxyMetrics{HTTP: resourcemetrics.HTTPMetrics{RequestsPerSecond: &rps, RequestsPeakPerSecond: &rpsPeak}},
		}},
	}, nil
}

func (usageStub) ReadProject(projectID string) (resourcemetrics.Current, error) {
	if projectID != "project" {
		return resourcemetrics.Current{}, cgroupstats.ErrInvalidResource
	}
	return usageStub{}.Read(cgroupstats.Service, "api")
}

func (usageStub) ProjectHistory(ctx context.Context, projectID string, window time.Duration) (resourcemetrics.History, error) {
	if projectID != "project" {
		return resourcemetrics.History{}, cgroupstats.ErrInvalidResource
	}
	return usageStub{}.History(ctx, cgroupstats.Service, "api", window)
}

func (usageStub) ReadInstallation() (resourcemetrics.Current, error) {
	current, err := usageStub{}.Read(cgroupstats.Service, "api")
	if err != nil {
		return resourcemetrics.Current{}, err
	}
	hostCPU, hostIngress, hostEgress := int64(2800), int64(300), int64(150)
	current.Host = &resourcemetrics.HostCurrent{
		ObservedAt: 42, CPUMillicores: &hostCPU, CPUCores: 8,
		MemoryUsedBytes: 8 << 30, MemoryTotalBytes: 16 << 30,
		NetworkIngressBytesPerSecond: &hostIngress, NetworkEgressBytesPerSecond: &hostEgress,
		NetworkInterface: "eth0",
	}
	return current, nil
}

func (usageStub) InstallationHistory(ctx context.Context, window time.Duration) (resourcemetrics.History, error) {
	return usageStub{}.History(ctx, cgroupstats.Service, "api", window)
}

func (usageStub) HostHistory(ctx context.Context, window time.Duration) (resourcemetrics.History, error) {
	return usageStub{}.History(ctx, cgroupstats.Service, "api", window)
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

func TestInfrastructureOmitsComponentTimestampUntilBackgroundScanCompletes(t *testing.T) {
	t.Parallel()
	capacity := pendingCapacityStub{pressureStub: pressureStub{
		ready: true,
		snapshot: diskpressure.Snapshot{
			CheckedAt: time.UnixMilli(42),
			Usage:     diskpressure.Usage{TotalBytes: 100, AvailableBytes: 50},
		},
	}}
	handler := access.ProtectAdmin(
		"admin.example.com",
		projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithDiskPressure(capacity)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/infrastructure/disk-pressure", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"components":[{"id":"volumes","bytes":0}]`) {
		t.Fatalf("disk pressure = %d/%s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), `"componentsCheckedAt"`) {
		t.Fatalf("pending component timestamp was published: %s", response.Body)
	}
}

func TestInfrastructureRunsSafeImageGarbageCollectionWithAccess(t *testing.T) {
	t.Parallel()
	collector := &imageGarbageCollectorStub{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithImageGarbageCollector(collector))
	path := "/api/v1/infrastructure/container-images/gc"
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
	if response.Code != http.StatusForbidden || collector.calls != 0 {
		t.Fatalf("direct image GC = %d/%s calls=%d", response.Code, response.Body, collector.calls)
	}
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	request := projectRequest(http.MethodPost, path, "")
	request.Header.Set("Origin", "https://admin.example.com")
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusOK || collector.calls != 1 ||
		!strings.Contains(response.Body.String(), `"finalImagesRemoved":2`) ||
		!strings.Contains(response.Body.String(), `"buildCacheImagesRemoved":3`) ||
		!strings.Contains(response.Body.String(), `"orphanLayersRemoved":1`) ||
		!strings.Contains(response.Body.String(), `"removedBytes":42`) {
		t.Fatalf("protected image GC = %d/%s calls=%d", response.Code, response.Body, collector.calls)
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
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"memoryBytes":67108864`) ||
		!strings.Contains(response.Body.String(), `"networkAvailable":true`) ||
		!strings.Contains(response.Body.String(), `"running":true`) ||
		!strings.Contains(response.Body.String(), `"cpuPeakMillicores":84`) ||
		!strings.Contains(response.Body.String(), `"requestsPeakPerSecond":40`) {
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
		!strings.Contains(response.Body.String(), `"networkIngressBytesPerSecond":34`) ||
		!strings.Contains(response.Body.String(), `"cpuPeakMillicores":90`) ||
		!strings.Contains(response.Body.String(), `"requestsPeakPerSecond":30`) {
		t.Fatalf("resource usage history = %d/%s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/infrastructure/resources/service/api/usage/history?range=2h", ""))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid resource usage range = %d/%s", response.Code, response.Body)
	}
}

func TestInfrastructureReportsProjectAndInstallationUsageFromSameSampler(t *testing.T) {
	t.Parallel()
	direct := server.Handler(server.DefaultMeta("ready"), server.WithResourceUsage(usageStub{}))
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	for _, path := range []string{
		"/api/v1/infrastructure/projects/project/usage",
		"/api/v1/infrastructure/usage",
	} {
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, projectRequest(http.MethodGet, path, ""))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cpuMillicores":42`) ||
			!strings.Contains(response.Body.String(), `"networkIngressBytesPerSecond":100`) ||
			!strings.Contains(response.Body.String(), `"runningResources":1`) ||
			!strings.Contains(response.Body.String(), `"requestsPerSecond":12.5`) {
			t.Fatalf("scope usage %s = %d/%s", path, response.Code, response.Body)
		}
		if path == "/api/v1/infrastructure/usage" && !strings.Contains(response.Body.String(), `"networkInterface":"eth0"`) {
			t.Fatalf("installation host usage = %d/%s", response.Code, response.Body)
		}
	}
	for _, path := range []string{
		"/api/v1/infrastructure/projects/project/usage/history?range=6h",
		"/api/v1/infrastructure/usage/history?range=6h",
		"/api/v1/infrastructure/host/usage/history?range=6h",
	} {
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, projectRequest(http.MethodGet, path, ""))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cpuMillicores":12`) {
			t.Fatalf("scope history %s = %d/%s", path, response.Code, response.Body)
		}
	}
}

func TestInfrastructureReadsBoundedPlatformJournalOnlyWithAccess(t *testing.T) {
	t.Parallel()
	logs := &journalStub{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithInfrastructureLogs(logs))
	path := "/api/v1/infrastructure/logs?limit=25&beforeCursor=opaque%3Bcursor%3Dvalue"
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusForbidden || logs.calls != 0 {
		t.Fatalf("direct journal = %d/%s calls=%d", response.Code, response.Body, logs.calls)
	}
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, path, ""))
	if response.Code != http.StatusOK || logs.calls != 1 || logs.query.Limit != 25 ||
		logs.query.BeforeCursor != "opaque;cursor=value" ||
		!strings.Contains(response.Body.String(), `"message":"platform ready"`) ||
		!strings.Contains(response.Body.String(), `"nextCursor":"next-cursor"`) {
		t.Fatalf("journal = %d/%s calls=%d query=%+v", response.Code, response.Body, logs.calls, logs.query)
	}
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/infrastructure/logs?limit=2001", ""))
	if response.Code != http.StatusBadRequest || logs.calls != 1 {
		t.Fatalf("invalid journal query = %d/%s calls=%d", response.Code, response.Body, logs.calls)
	}
}
