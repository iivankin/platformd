package state

import (
	"context"
	"fmt"

	"github.com/iivankin/platformd/internal/trafficmetrics"
)

type ResourceMetricTarget struct {
	Kind       string
	ResourceID string
	ProjectID  string
	HTTPRoute  bool
	TCPRoute   bool
	UDPRoute   bool
	// AggregatePublicTraffic is true for services and import gateways. Export
	// gateways dual-attribute bytes to their target service, so they stay false
	// to avoid double-counting project/installation rollups.
	AggregatePublicTraffic bool
}

type ResourceMetricSample struct {
	Kind       string
	ResourceID string
	ObservedAt int64
	MetricValues
}

type ProxyMetricSample struct {
	HTTP HTTPMetricSample `json:"http"`
	TCP  TCPMetricSample  `json:"tcp"`
	UDP  UDPMetricSample  `json:"udp"`
}

type HTTPMetricSample struct {
	RequestsPerSecond     *float64                                       `json:"requestsPerSecond,omitempty"`
	RequestsPeakPerSecond *float64                                       `json:"requestsPeakPerSecond,omitempty"`
	RequestsTotal         uint64                                         `json:"requestsTotal"`
	ActiveRequests        int64                                          `json:"activeRequests"`
	ActiveRequestsPeak    int64                                          `json:"activeRequestsPeak"`
	Responses2xxPerSecond *float64                                       `json:"responses2xxPerSecond,omitempty"`
	Responses3xxPerSecond *float64                                       `json:"responses3xxPerSecond,omitempty"`
	Responses4xxPerSecond *float64                                       `json:"responses4xxPerSecond,omitempty"`
	Responses5xxPerSecond *float64                                       `json:"responses5xxPerSecond,omitempty"`
	DurationBuckets       [trafficmetrics.HTTPDurationBucketCount]uint64 `json:"durationBuckets"`
}

type TCPMetricSample struct {
	ConnectionsPerSecond     *float64 `json:"connectionsPerSecond,omitempty"`
	ConnectionsPeakPerSecond *float64 `json:"connectionsPeakPerSecond,omitempty"`
	ConnectionsTotal         uint64   `json:"connectionsTotal"`
	ActiveConnections        int64    `json:"activeConnections"`
	ActiveConnectionsPeak    int64    `json:"activeConnectionsPeak"`
}

type UDPMetricSample struct {
	IngressPacketsPerSecond     *float64 `json:"ingressPacketsPerSecond,omitempty"`
	IngressPacketsPeakPerSecond *float64 `json:"ingressPacketsPeakPerSecond,omitempty"`
	EgressPacketsPerSecond      *float64 `json:"egressPacketsPerSecond,omitempty"`
	EgressPacketsPeakPerSecond  *float64 `json:"egressPacketsPeakPerSecond,omitempty"`
	IngressPacketsTotal         uint64   `json:"ingressPacketsTotal"`
	EgressPacketsTotal          uint64   `json:"egressPacketsTotal"`
}

type MetricValues struct {
	DurationMillis                   int64
	CPUDurationMillis                int64
	NetworkDurationMillis            int64
	ProxyDurationMillis              int64
	CPUMillicores                    *int64
	CPUPeakMillicores                *int64
	MemoryBytes                      uint64
	MemoryPeakBytes                  uint64
	DiskBytes                        *uint64
	NetworkIngressBytesPerSecond     *int64
	NetworkIngressPeakBytesPerSecond *int64
	NetworkEgressBytesPerSecond      *int64
	NetworkEgressPeakBytesPerSecond  *int64
	Running                          bool
	Proxy                            *ProxyMetricSample
}

type MetricBatch struct {
	Resources       []ResourceMetricSample
	Aggregates      []AggregateMetricSample
	RetentionCutoff int64
}

type AggregateMetricSample struct {
	ScopeKind        string
	ScopeID          string
	ObservedAt       int64
	RunningResources int
	TotalResources   int
	MetricValues
}

func (store *Store) ResourceMetricTargets(ctx context.Context) ([]ResourceMetricTarget, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT 'service', s.id, s.project_id,
  EXISTS (SELECT 1 FROM service_domains d WHERE d.service_id = s.id)
    OR EXISTS (SELECT 1 FROM preview_deployments p WHERE p.service_id = s.id AND p.status = 'active'),
  EXISTS (SELECT 1 FROM service_listeners l WHERE l.service_id = s.id AND l.protocol = 'tcp')
    OR EXISTS (SELECT 1 FROM network_gateways g WHERE g.target_service_id = s.id AND g.mode = 'export' AND g.protocol = 'tcp'),
  EXISTS (SELECT 1 FROM service_listeners l WHERE l.service_id = s.id AND l.protocol = 'udp')
    OR EXISTS (SELECT 1 FROM network_gateways g WHERE g.target_service_id = s.id AND g.mode = 'export' AND g.protocol = 'udp'),
  1
FROM services s WHERE s.enabled = 1
UNION ALL SELECT 'postgres', id, project_id, 0, 0, 0, 0 FROM managed_postgres
UNION ALL SELECT 'redis', id, project_id, 0, 0, 0, 0 FROM managed_redis
UNION ALL SELECT 'network_gateway', id, project_id, 0, protocol = 'tcp', protocol = 'udp', mode = 'import' FROM network_gateways
ORDER BY 1, 2`)
	if err != nil {
		return nil, fmt.Errorf("list metric targets: %w", err)
	}
	defer rows.Close()
	targets := make([]ResourceMetricTarget, 0)
	for rows.Next() {
		var target ResourceMetricTarget
		if err := rows.Scan(
			&target.Kind, &target.ResourceID, &target.ProjectID,
			&target.HTTPRoute, &target.TCPRoute, &target.UDPRoute,
			&target.AggregatePublicTraffic,
		); err != nil {
			return nil, fmt.Errorf("scan metric target: %w", err)
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (store *Store) ResourceMetricProjectIDs(ctx context.Context) ([]string, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT id FROM projects ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list metric projects: %w", err)
	}
	defer rows.Close()
	projects := make([]string, 0)
	for rows.Next() {
		var projectID string
		if err := rows.Scan(&projectID); err != nil {
			return nil, fmt.Errorf("scan metric project: %w", err)
		}
		projects = append(projects, projectID)
	}
	return projects, rows.Err()
}
