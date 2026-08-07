package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/iivankin/platformd/internal/trafficmetrics"
)

const maximumMetricCounter = uint64(math.MaxInt64)

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

type AggregateMetricSample struct {
	ScopeKind        string
	ScopeID          string
	ObservedAt       int64
	RunningResources int
	TotalResources   int
	MetricValues
}

func (store *Store) RecordMetricBatch(ctx context.Context, batch MetricBatch) error {
	if batch.RetentionCutoff <= 0 {
		return errors.New("metric retention cutoff is invalid")
	}
	for _, sample := range batch.Resources {
		if err := validateResourceMetricSample(sample); err != nil {
			return err
		}
	}
	for _, sample := range batch.Aggregates {
		if err := validateAggregateMetricSample(sample); err != nil {
			return err
		}
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		if err := recordResourceMetricSamples(ctx, transaction, batch.Resources); err != nil {
			return err
		}
		if err := recordAggregateMetricSamples(ctx, transaction, batch.Aggregates); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM resource_metric_samples WHERE observed_at < ?", batch.RetentionCutoff); err != nil {
			return fmt.Errorf("delete expired resource metric samples: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM aggregate_metric_samples WHERE observed_at < ?", batch.RetentionCutoff); err != nil {
			return fmt.Errorf("delete expired aggregate metric samples: %w", err)
		}
		return nil
	})
}

func recordResourceMetricSamples(ctx context.Context, transaction *sql.Tx, samples []ResourceMetricSample) error {
	statement, err := transaction.PrepareContext(ctx, `
INSERT INTO resource_metric_samples(
  resource_kind, resource_id, observed_at, duration_millis,
  cpu_duration_millis, network_duration_millis, proxy_duration_millis,
  cpu_millicores, cpu_peak_millicores, memory_bytes, memory_peak_bytes, disk_bytes,
  network_ingress_bytes_per_second, network_ingress_peak_bytes_per_second,
  network_egress_bytes_per_second, network_egress_peak_bytes_per_second,
  running, proxy_metrics_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(resource_kind, resource_id, observed_at) DO UPDATE SET
  duration_millis = excluded.duration_millis,
  cpu_duration_millis = excluded.cpu_duration_millis,
  network_duration_millis = excluded.network_duration_millis,
  proxy_duration_millis = excluded.proxy_duration_millis,
  cpu_millicores = excluded.cpu_millicores,
  cpu_peak_millicores = excluded.cpu_peak_millicores,
  memory_bytes = excluded.memory_bytes,
  memory_peak_bytes = excluded.memory_peak_bytes,
  disk_bytes = excluded.disk_bytes,
  network_ingress_bytes_per_second = excluded.network_ingress_bytes_per_second,
  network_ingress_peak_bytes_per_second = excluded.network_ingress_peak_bytes_per_second,
  network_egress_bytes_per_second = excluded.network_egress_bytes_per_second,
  network_egress_peak_bytes_per_second = excluded.network_egress_peak_bytes_per_second,
  running = excluded.running,
  proxy_metrics_json = excluded.proxy_metrics_json`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, sample := range samples {
		proxyJSON, err := encodeProxyMetricSample(sample.Proxy)
		if err != nil {
			return err
		}
		if _, err := statement.ExecContext(ctx,
			sample.Kind, sample.ResourceID, sample.ObservedAt, sample.DurationMillis,
			sample.CPUDurationMillis, sample.NetworkDurationMillis, sample.ProxyDurationMillis,
			nullableInt64(sample.CPUMillicores), nullableInt64(sample.CPUPeakMillicores),
			int64(sample.MemoryBytes), int64(sample.MemoryPeakBytes), nullableUint64(sample.DiskBytes),
			nullableInt64(sample.NetworkIngressBytesPerSecond), nullableInt64(sample.NetworkIngressPeakBytesPerSecond),
			nullableInt64(sample.NetworkEgressBytesPerSecond), nullableInt64(sample.NetworkEgressPeakBytesPerSecond),
			boolInteger(sample.Running), proxyJSON,
		); err != nil {
			return fmt.Errorf("record resource metric sample: %w", err)
		}
	}
	return nil
}

func recordAggregateMetricSamples(ctx context.Context, transaction *sql.Tx, samples []AggregateMetricSample) error {
	statement, err := transaction.PrepareContext(ctx, `
INSERT INTO aggregate_metric_samples(
  scope_kind, scope_id, observed_at, duration_millis,
  cpu_duration_millis, network_duration_millis, proxy_duration_millis,
  cpu_millicores, cpu_peak_millicores, memory_bytes, memory_peak_bytes, disk_bytes,
  network_ingress_bytes_per_second, network_ingress_peak_bytes_per_second,
  network_egress_bytes_per_second, network_egress_peak_bytes_per_second,
  running_resources, total_resources, proxy_metrics_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scope_kind, scope_id, observed_at) DO UPDATE SET
  duration_millis = excluded.duration_millis,
  cpu_duration_millis = excluded.cpu_duration_millis,
  network_duration_millis = excluded.network_duration_millis,
  proxy_duration_millis = excluded.proxy_duration_millis,
  cpu_millicores = excluded.cpu_millicores,
  cpu_peak_millicores = excluded.cpu_peak_millicores,
  memory_bytes = excluded.memory_bytes,
  memory_peak_bytes = excluded.memory_peak_bytes,
  disk_bytes = excluded.disk_bytes,
  network_ingress_bytes_per_second = excluded.network_ingress_bytes_per_second,
  network_ingress_peak_bytes_per_second = excluded.network_ingress_peak_bytes_per_second,
  network_egress_bytes_per_second = excluded.network_egress_bytes_per_second,
  network_egress_peak_bytes_per_second = excluded.network_egress_peak_bytes_per_second,
  running_resources = excluded.running_resources,
  total_resources = excluded.total_resources,
  proxy_metrics_json = excluded.proxy_metrics_json`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, sample := range samples {
		proxyJSON, err := encodeProxyMetricSample(sample.Proxy)
		if err != nil {
			return err
		}
		if _, err := statement.ExecContext(ctx,
			sample.ScopeKind, sample.ScopeID, sample.ObservedAt, sample.DurationMillis,
			sample.CPUDurationMillis, sample.NetworkDurationMillis, sample.ProxyDurationMillis,
			nullableInt64(sample.CPUMillicores), nullableInt64(sample.CPUPeakMillicores),
			int64(sample.MemoryBytes), int64(sample.MemoryPeakBytes), nullableUint64(sample.DiskBytes),
			nullableInt64(sample.NetworkIngressBytesPerSecond), nullableInt64(sample.NetworkIngressPeakBytesPerSecond),
			nullableInt64(sample.NetworkEgressBytesPerSecond), nullableInt64(sample.NetworkEgressPeakBytesPerSecond),
			sample.RunningResources, sample.TotalResources, proxyJSON,
		); err != nil {
			return fmt.Errorf("record aggregate metric sample: %w", err)
		}
	}
	return nil
}

func (store *Store) AggregateMetricSamples(ctx context.Context, scopeKind, scopeID string, from, to int64) ([]AggregateMetricSample, error) {
	if !validAggregateMetricScope(scopeKind, scopeID) || from <= 0 || to < from {
		return nil, errors.New("aggregate metric query is invalid")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT observed_at, cpu_millicores, memory_bytes,
       duration_millis, cpu_duration_millis, network_duration_millis, proxy_duration_millis,
       cpu_peak_millicores, memory_peak_bytes, disk_bytes,
       network_ingress_bytes_per_second, network_ingress_peak_bytes_per_second,
       network_egress_bytes_per_second, network_egress_peak_bytes_per_second,
       running_resources, total_resources, proxy_metrics_json
FROM aggregate_metric_samples
WHERE scope_kind = ? AND scope_id = ? AND observed_at BETWEEN ? AND ?
ORDER BY observed_at`, scopeKind, scopeID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query aggregate metric samples: %w", err)
	}
	defer rows.Close()
	result := make([]AggregateMetricSample, 0)
	for rows.Next() {
		sample, err := scanAggregateMetricSample(rows, scopeKind, scopeID)
		if err != nil {
			return nil, fmt.Errorf("scan aggregate metric sample: %w", err)
		}
		result = append(result, sample)
	}
	return result, rows.Err()
}

func scanAggregateMetricSample(scanner metricScanner, scopeKind, scopeID string, trailing ...any) (AggregateMetricSample, error) {
	sample := AggregateMetricSample{ScopeKind: scopeKind, ScopeID: scopeID}
	var cpu, cpuPeak, disk, ingress, ingressPeak, egress, egressPeak sql.NullInt64
	var proxyJSON sql.NullString
	var memory, memoryPeak int64
	destinations := []any{
		&sample.ObservedAt, &cpu, &memory, &sample.DurationMillis,
		&sample.CPUDurationMillis, &sample.NetworkDurationMillis, &sample.ProxyDurationMillis,
		&cpuPeak, &memoryPeak, &disk,
		&ingress, &ingressPeak, &egress, &egressPeak,
		&sample.RunningResources, &sample.TotalResources, &proxyJSON,
	}
	if err := scanner.Scan(append(destinations, trailing...)...); err != nil {
		return AggregateMetricSample{}, err
	}
	sample.MemoryBytes, sample.MemoryPeakBytes = uint64(memory), uint64(memoryPeak)
	sample.DiskBytes = nullUint64Pointer(disk)
	sample.CPUMillicores = nullInt64Pointer(cpu)
	sample.CPUPeakMillicores = nullInt64Pointer(cpuPeak)
	sample.NetworkIngressBytesPerSecond = nullInt64Pointer(ingress)
	sample.NetworkIngressPeakBytesPerSecond = nullInt64Pointer(ingressPeak)
	sample.NetworkEgressBytesPerSecond = nullInt64Pointer(egress)
	sample.NetworkEgressPeakBytesPerSecond = nullInt64Pointer(egressPeak)
	var err error
	sample.Proxy, err = decodeProxyMetricSample(proxyJSON)
	return sample, err
}

func validateAggregateMetricSample(sample AggregateMetricSample) error {
	if !validAggregateMetricScope(sample.ScopeKind, sample.ScopeID) || sample.ObservedAt <= 0 ||
		sample.RunningResources < 0 ||
		sample.TotalResources < sample.RunningResources ||
		validateMetricValues(sample.MetricValues) != nil {
		return errors.New("aggregate metric sample is invalid")
	}
	return nil
}

func validAggregateMetricScope(kind, id string) bool {
	return (kind == "project" || kind == "installation" || kind == "host") && id != ""
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableUint64(value *uint64) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func nullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullUint64Pointer(value sql.NullInt64) *uint64 {
	if !value.Valid {
		return nil
	}
	converted := uint64(value.Int64)
	return &converted
}

func (store *Store) ResourceMetricSamples(ctx context.Context, kind, resourceID string, from, to int64) ([]ResourceMetricSample, error) {
	if !validMetricTarget(kind, resourceID) || from <= 0 || to < from {
		return nil, errors.New("resource metric query is invalid")
	}
	samples := make([]ResourceMetricSample, 0)
	rows, err := store.database.QueryContext(ctx, `
SELECT observed_at, duration_millis, cpu_duration_millis, network_duration_millis, proxy_duration_millis,
       cpu_millicores, cpu_peak_millicores,
       memory_bytes, memory_peak_bytes, disk_bytes,
       network_ingress_bytes_per_second, network_ingress_peak_bytes_per_second,
       network_egress_bytes_per_second, network_egress_peak_bytes_per_second,
       running, proxy_metrics_json
FROM resource_metric_samples
WHERE resource_kind = ? AND resource_id = ? AND observed_at BETWEEN ? AND ?
ORDER BY observed_at`, kind, resourceID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query resource metric samples: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		sample, err := scanResourceMetricSample(rows, kind, resourceID)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

type metricScanner interface {
	Scan(...any) error
}

func scanResourceMetricSample(scanner metricScanner, kind, resourceID string, trailing ...any) (ResourceMetricSample, error) {
	var sample ResourceMetricSample
	var cpu, cpuPeak, disk, ingress, ingressPeak, egress, egressPeak sql.NullInt64
	var memory, memoryPeak int64
	var proxyJSON sql.NullString
	var running int
	destinations := []any{
		&sample.ObservedAt, &sample.DurationMillis,
		&sample.CPUDurationMillis, &sample.NetworkDurationMillis, &sample.ProxyDurationMillis,
		&cpu, &cpuPeak, &memory, &memoryPeak, &disk,
		&ingress, &ingressPeak, &egress, &egressPeak, &running, &proxyJSON,
	}
	if err := scanner.Scan(append(destinations, trailing...)...); err != nil {
		return ResourceMetricSample{}, err
	}
	sample.Kind, sample.ResourceID = kind, resourceID
	sample.CPUMillicores = nullInt64Pointer(cpu)
	sample.CPUPeakMillicores = nullInt64Pointer(cpuPeak)
	sample.MemoryBytes, sample.MemoryPeakBytes = uint64(memory), uint64(memoryPeak)
	sample.DiskBytes = nullUint64Pointer(disk)
	sample.NetworkIngressBytesPerSecond = nullInt64Pointer(ingress)
	sample.NetworkIngressPeakBytesPerSecond = nullInt64Pointer(ingressPeak)
	sample.NetworkEgressBytesPerSecond = nullInt64Pointer(egress)
	sample.NetworkEgressPeakBytesPerSecond = nullInt64Pointer(egressPeak)
	sample.Running = running == 1
	var err error
	sample.Proxy, err = decodeProxyMetricSample(proxyJSON)
	if err != nil {
		return ResourceMetricSample{}, err
	}
	return sample, nil
}

func validateResourceMetricSample(sample ResourceMetricSample) error {
	if !validMetricTarget(sample.Kind, sample.ResourceID) || sample.ObservedAt <= 0 ||
		validateMetricValues(sample.MetricValues) != nil {
		return errors.New("resource metric sample is invalid")
	}
	return nil
}

func validMetricTarget(kind, resourceID string) bool {
	return (kind == "service" || kind == "postgres" || kind == "redis" || kind == "network_gateway") && resourceID != ""
}

func encodeProxyMetricSample(sample *ProxyMetricSample) (any, error) {
	if sample == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(sample)
	if err != nil {
		return nil, fmt.Errorf("encode proxy metric sample: %w", err)
	}
	return string(encoded), nil
}

func decodeProxyMetricSample(encoded sql.NullString) (*ProxyMetricSample, error) {
	if !encoded.Valid {
		return nil, nil
	}
	var sample ProxyMetricSample
	if err := json.Unmarshal([]byte(encoded.String), &sample); err != nil || !validProxyMetricSample(&sample) {
		return nil, errors.New("stored proxy metric sample is invalid")
	}
	return &sample, nil
}

func validProxyMetricSample(sample *ProxyMetricSample) bool {
	if sample == nil {
		return true
	}
	if sample.HTTP.RequestsTotal > maximumMetricCounter || sample.TCP.ConnectionsTotal > maximumMetricCounter ||
		sample.UDP.IngressPacketsTotal > maximumMetricCounter || sample.UDP.EgressPacketsTotal > maximumMetricCounter ||
		sample.HTTP.ActiveRequests < 0 || sample.HTTP.ActiveRequestsPeak < sample.HTTP.ActiveRequests ||
		sample.TCP.ActiveConnections < 0 || sample.TCP.ActiveConnectionsPeak < sample.TCP.ActiveConnections {
		return false
	}
	for _, value := range []*float64{
		sample.HTTP.RequestsPerSecond, sample.HTTP.RequestsPeakPerSecond,
		sample.HTTP.Responses2xxPerSecond, sample.HTTP.Responses3xxPerSecond,
		sample.HTTP.Responses4xxPerSecond, sample.HTTP.Responses5xxPerSecond,
		sample.TCP.ConnectionsPerSecond, sample.TCP.ConnectionsPeakPerSecond,
		sample.UDP.IngressPacketsPerSecond, sample.UDP.IngressPacketsPeakPerSecond,
		sample.UDP.EgressPacketsPerSecond, sample.UDP.EgressPacketsPeakPerSecond,
	} {
		if value != nil && (*value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return false
		}
	}
	if !validFloatPeak(sample.HTTP.RequestsPerSecond, sample.HTTP.RequestsPeakPerSecond) ||
		!validFloatPeak(sample.TCP.ConnectionsPerSecond, sample.TCP.ConnectionsPeakPerSecond) ||
		!validFloatPeak(sample.UDP.IngressPacketsPerSecond, sample.UDP.IngressPacketsPeakPerSecond) ||
		!validFloatPeak(sample.UDP.EgressPacketsPerSecond, sample.UDP.EgressPacketsPeakPerSecond) {
		return false
	}
	var durationTotal uint64
	for _, count := range sample.HTTP.DurationBuckets {
		if count > maximumMetricCounter-durationTotal {
			return false
		}
		durationTotal += count
	}
	return true
}

func validateMetricValues(values MetricValues) error {
	if values.DurationMillis <= 0 || values.MemoryBytes > maximumMetricCounter ||
		values.MemoryPeakBytes > maximumMetricCounter || values.MemoryPeakBytes < values.MemoryBytes ||
		(values.DiskBytes != nil && *values.DiskBytes > maximumMetricCounter) ||
		!validMetricCoverage(values.CPUDurationMillis, values.DurationMillis, values.CPUMillicores != nil) ||
		!validMetricCoverage(values.NetworkDurationMillis, values.DurationMillis, values.NetworkIngressBytesPerSecond != nil) ||
		values.ProxyDurationMillis < 0 || values.ProxyDurationMillis > values.DurationMillis ||
		(values.ProxyDurationMillis > 0 && values.Proxy == nil) ||
		(values.ProxyDurationMillis == 0 && proxyMetricHasRates(values.Proxy)) ||
		!validIntPeak(values.CPUMillicores, values.CPUPeakMillicores) ||
		!validIntPeak(values.NetworkIngressBytesPerSecond, values.NetworkIngressPeakBytesPerSecond) ||
		!validIntPeak(values.NetworkEgressBytesPerSecond, values.NetworkEgressPeakBytesPerSecond) ||
		(values.NetworkIngressBytesPerSecond == nil) != (values.NetworkEgressBytesPerSecond == nil) ||
		!validProxyMetricSample(values.Proxy) {
		return errors.New("metric values are invalid")
	}
	return nil
}

func validMetricCoverage(coverage, total int64, available bool) bool {
	return coverage >= 0 && coverage <= total && (coverage > 0) == available
}

func proxyMetricHasRates(sample *ProxyMetricSample) bool {
	if sample == nil {
		return false
	}
	return sample.HTTP.RequestsPerSecond != nil || sample.HTTP.Responses2xxPerSecond != nil ||
		sample.HTTP.Responses3xxPerSecond != nil || sample.HTTP.Responses4xxPerSecond != nil ||
		sample.HTTP.Responses5xxPerSecond != nil || sample.TCP.ConnectionsPerSecond != nil ||
		sample.UDP.IngressPacketsPerSecond != nil || sample.UDP.EgressPacketsPerSecond != nil
}

func validIntPeak(average, peak *int64) bool {
	if (average == nil) != (peak == nil) {
		return false
	}
	return average == nil || (*average >= 0 && *peak >= *average)
}

func validFloatPeak(average, peak *float64) bool {
	if (average == nil) != (peak == nil) {
		return false
	}
	return average == nil || *peak >= *average
}
