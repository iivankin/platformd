package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

const maximumMetricCounter = uint64(math.MaxInt64)

type ResourceMetricTarget struct {
	Kind       string
	ResourceID string
}

type ResourceMetricSample struct {
	Kind           string
	ResourceID     string
	ObservedAt     int64
	CPUUsageMicros uint64
	MemoryBytes    uint64
	NetworkRXBytes *uint64
	NetworkTXBytes *uint64
	Running        bool
}

func (store *Store) ResourceMetricTargets(ctx context.Context) ([]ResourceMetricTarget, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT 'service', id FROM services WHERE enabled = 1
UNION ALL SELECT 'postgres', id FROM managed_postgres
UNION ALL SELECT 'redis', id FROM managed_redis
ORDER BY 1, 2`)
	if err != nil {
		return nil, fmt.Errorf("list metric targets: %w", err)
	}
	defer rows.Close()
	targets := make([]ResourceMetricTarget, 0)
	for rows.Next() {
		var target ResourceMetricTarget
		if err := rows.Scan(&target.Kind, &target.ResourceID); err != nil {
			return nil, fmt.Errorf("scan metric target: %w", err)
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (store *Store) RecordResourceMetricSamples(ctx context.Context, samples []ResourceMetricSample) error {
	for _, sample := range samples {
		if err := validateResourceMetricSample(sample); err != nil {
			return err
		}
	}
	if len(samples) == 0 {
		return nil
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		statement, err := transaction.PrepareContext(ctx, `
INSERT INTO resource_metric_samples(
  resource_kind, resource_id, observed_at, cpu_usage_micros, memory_bytes,
  network_rx_bytes, network_tx_bytes, running
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(resource_kind, resource_id, observed_at) DO UPDATE SET
  cpu_usage_micros = excluded.cpu_usage_micros,
  memory_bytes = excluded.memory_bytes,
  network_rx_bytes = excluded.network_rx_bytes,
  network_tx_bytes = excluded.network_tx_bytes,
  running = excluded.running`)
		if err != nil {
			return err
		}
		defer statement.Close()
		for _, sample := range samples {
			if _, err := statement.ExecContext(
				ctx, sample.Kind, sample.ResourceID, sample.ObservedAt,
				int64(sample.CPUUsageMicros), int64(sample.MemoryBytes),
				nullableMetricCounter(sample.NetworkRXBytes), nullableMetricCounter(sample.NetworkTXBytes),
				boolInteger(sample.Running),
			); err != nil {
				return fmt.Errorf("record resource metric sample: %w", err)
			}
		}
		return nil
	})
}

func (store *Store) ResourceMetricSamples(ctx context.Context, kind, resourceID string, from, to int64) ([]ResourceMetricSample, error) {
	if !validMetricTarget(kind, resourceID) || from <= 0 || to < from {
		return nil, errors.New("resource metric query is invalid")
	}
	samples := make([]ResourceMetricSample, 0)
	previous, found, err := store.resourceMetricSampleBefore(ctx, kind, resourceID, from)
	if err != nil {
		return nil, err
	}
	if found {
		samples = append(samples, previous)
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT observed_at, cpu_usage_micros, memory_bytes, network_rx_bytes, network_tx_bytes, running
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

func (store *Store) DeleteResourceMetricSamplesBefore(ctx context.Context, cutoff int64) error {
	if cutoff <= 0 {
		return errors.New("resource metric retention cutoff is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, "DELETE FROM resource_metric_samples WHERE observed_at < ?", cutoff)
		return err
	})
}

func (store *Store) resourceMetricSampleBefore(ctx context.Context, kind, resourceID string, before int64) (ResourceMetricSample, bool, error) {
	row := store.database.QueryRowContext(ctx, `
SELECT observed_at, cpu_usage_micros, memory_bytes, network_rx_bytes, network_tx_bytes, running
FROM resource_metric_samples
WHERE resource_kind = ? AND resource_id = ? AND observed_at < ?
ORDER BY observed_at DESC LIMIT 1`, kind, resourceID, before)
	sample, err := scanResourceMetricSample(row, kind, resourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ResourceMetricSample{}, false, nil
	}
	return sample, err == nil, err
}

type metricScanner interface {
	Scan(...any) error
}

func scanResourceMetricSample(scanner metricScanner, kind, resourceID string) (ResourceMetricSample, error) {
	var sample ResourceMetricSample
	var cpu, memory int64
	var networkRX, networkTX sql.NullInt64
	var running int
	if err := scanner.Scan(&sample.ObservedAt, &cpu, &memory, &networkRX, &networkTX, &running); err != nil {
		return ResourceMetricSample{}, err
	}
	sample.Kind, sample.ResourceID = kind, resourceID
	sample.CPUUsageMicros, sample.MemoryBytes = uint64(cpu), uint64(memory)
	if networkRX.Valid {
		value := uint64(networkRX.Int64)
		sample.NetworkRXBytes = &value
	}
	if networkTX.Valid {
		value := uint64(networkTX.Int64)
		sample.NetworkTXBytes = &value
	}
	sample.Running = running == 1
	return sample, nil
}

func validateResourceMetricSample(sample ResourceMetricSample) error {
	if !validMetricTarget(sample.Kind, sample.ResourceID) || sample.ObservedAt <= 0 ||
		sample.CPUUsageMicros > maximumMetricCounter || sample.MemoryBytes > maximumMetricCounter ||
		(sample.NetworkRXBytes != nil && *sample.NetworkRXBytes > maximumMetricCounter) ||
		(sample.NetworkTXBytes != nil && *sample.NetworkTXBytes > maximumMetricCounter) {
		return errors.New("resource metric sample is invalid")
	}
	if (sample.NetworkRXBytes == nil) != (sample.NetworkTXBytes == nil) {
		return errors.New("resource metric network counters must be recorded together")
	}
	return nil
}

func validMetricTarget(kind, resourceID string) bool {
	return (kind == "service" || kind == "postgres" || kind == "redis") && resourceID != ""
}

func nullableMetricCounter(value *uint64) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}
