package resourcemetrics

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/hostmetrics"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

const (
	LiveSampleInterval = 2 * time.Second
	PersistInterval    = time.Minute
	Retention          = 30 * 24 * time.Hour
)

var (
	ErrInvalidRange = errors.New("invalid resource metrics range")
	ErrNotReady     = errors.New("resource metrics are not ready")
)

type Store interface {
	ResourceMetricTargets(context.Context) ([]state.ResourceMetricTarget, error)
	ResourceMetricProjectIDs(context.Context) ([]string, error)
	RecordMetricBatch(context.Context, state.MetricBatch) error
	ResourceMetricSamples(context.Context, string, string, int64, int64) ([]state.ResourceMetricSample, error)
	AggregateMetricSamples(context.Context, string, string, int64, int64) ([]state.AggregateMetricSample, error)
}

type UsageReader interface {
	Read(cgroupstats.Kind, string) (cgroupstats.Sample, error)
}

// NetworkReader returns all public counters in one operation. Production uses
// one nftables netlink dump plus a lock-free snapshot of proxy counters.
type NetworkReader interface {
	PublicNetworkCounters() (PublicNetworkSnapshot, error)
}

// PublicNetworkSnapshot keeps proxy counters usable when the independent
// nftables byte-counter read fails. Complete is true only when Counters has
// both proxy and directly forwarded public payload bytes.
type PublicNetworkSnapshot struct {
	Counters map[string]trafficmetrics.Counters
	Complete bool
}

type ProxyMetrics struct {
	HTTP HTTPMetrics
	TCP  TCPMetrics
	UDP  UDPMetrics
}

type HTTPMetrics struct {
	RequestsPerSecond     *float64
	RequestsPeakPerSecond *float64
	RequestsTotal         uint64
	ActiveRequests        int64
	ActiveRequestsPeak    int64
	Responses2xxPerSecond *float64
	Responses3xxPerSecond *float64
	Responses4xxPerSecond *float64
	Responses5xxPerSecond *float64
	LatencyP50Millis      *float64
	LatencyP95Millis      *float64
	LatencyP99Millis      *float64
}

type TCPMetrics struct {
	ConnectionsPerSecond     *float64
	ConnectionsPeakPerSecond *float64
	ConnectionsTotal         uint64
	ActiveConnections        int64
	ActiveConnectionsPeak    int64
}

type UDPMetrics struct {
	IngressPacketsPerSecond     *float64
	IngressPacketsPeakPerSecond *float64
	EgressPacketsPerSecond      *float64
	EgressPacketsPeakPerSecond  *float64
	IngressPacketsTotal         uint64
	EgressPacketsTotal          uint64
}

type HostCurrent struct {
	ObservedAt                       int64
	CPUMillicores                    *int64
	CPUPeakMillicores                *int64
	CPUCores                         int
	MemoryUsedBytes                  uint64
	MemoryPeakBytes                  uint64
	MemoryTotalBytes                 uint64
	NetworkIngressBytesPerSecond     *int64
	NetworkIngressPeakBytesPerSecond *int64
	NetworkEgressBytesPerSecond      *int64
	NetworkEgressPeakBytesPerSecond  *int64
	NetworkInterface                 string
	interval                         metricInterval
}

type Current struct {
	cgroupstats.Sample
	NetworkRXBytes                   uint64
	NetworkTXBytes                   uint64
	NetworkAvailable                 bool
	CPUMillicores                    *int64
	CPUPeakMillicores                *int64
	MemoryPeakBytes                  uint64
	NetworkIngressBytesPerSecond     *int64
	NetworkIngressPeakBytesPerSecond *int64
	NetworkEgressBytesPerSecond      *int64
	NetworkEgressPeakBytesPerSecond  *int64
	RunningResources                 int
	TotalResources                   int
	Proxy                            *ProxyMetrics
	Host                             *HostCurrent
	httpDurationBuckets              [trafficmetrics.HTTPDurationBucketCount]uint64
	interval                         metricInterval
}

type Point struct {
	ObservedAt                       int64
	DurationMillis                   int64
	CPUMillicores                    *int64
	CPUPeakMillicores                *int64
	MemoryBytes                      uint64
	MemoryPeakBytes                  uint64
	NetworkIngressBytesPerSecond     *int64
	NetworkIngressPeakBytesPerSecond *int64
	NetworkEgressBytesPerSecond      *int64
	NetworkEgressPeakBytesPerSecond  *int64
	Running                          bool
	Proxy                            *ProxyMetrics
}

type History struct {
	From       int64
	To         int64
	StepMillis int64
	Points     []Point
}

type Config struct {
	LiveInterval    time.Duration
	PersistInterval time.Duration
	Retention       time.Duration
	Now             func() time.Time
}

type metricKey struct {
	kind string
	id   string
}

type previousSample struct {
	at               time.Time
	cpuUsageMicros   uint64
	networkIngress   uint64
	networkEgress    uint64
	networkAvailable bool
	running          bool
	traffic          trafficmetrics.Counters
}

type previousHostSample struct {
	at     time.Time
	sample hostmetrics.Sample
}

type metricInterval struct {
	duration            time.Duration
	cpuUsageMicros      *uint64
	networkIngressBytes *uint64
	networkEgressBytes  *uint64
	traffic             *trafficDelta
}

type trafficDelta struct {
	httpRequests, http2xx, http3xx, http4xx, http5xx uint64
	tcpConnections                                   uint64
	udpIngress, udpEgress                            uint64
	httpDurationBuckets                              [trafficmetrics.HTTPDurationBucketCount]uint64
}

type aggregateBuilder struct {
	current          Current
	cpuComplete      bool
	networkComplete  bool
	publicServices   int
	missingResources int
	proxyComplete    bool
}

type Application struct {
	store           Store
	usage           UsageReader
	network         NetworkReader
	host            hostmetrics.Reader
	liveInterval    time.Duration
	persistInterval time.Duration
	retention       time.Duration
	now             func() time.Time

	mu           sync.RWMutex
	resources    map[metricKey]Current
	projects     map[string]Current
	installation Current
	ready        bool
	previous     map[metricKey]previousSample
	previousHost *previousHostSample
	rollup       *minuteAccumulator
}
