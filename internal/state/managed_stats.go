package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ManagedStatSample struct {
	Kind        string // postgres|redis|object_store
	ResourceID  string
	ObservedAt  int64
	MetricsJSON string
}

func (store *Store) RecordManagedStatBatch(ctx context.Context, samples []ManagedStatSample, retentionCutoff int64) error {
	if retentionCutoff <= 0 {
		return errors.New("managed stat retention cutoff is invalid")
	}
	for _, sample := range samples {
		if err := validateManagedStatSample(sample); err != nil {
			return err
		}
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		if len(samples) > 0 {
			statement, err := transaction.PrepareContext(ctx, `
INSERT INTO managed_stat_samples(resource_kind, resource_id, observed_at, metrics_json)
VALUES (?, ?, ?, ?)
ON CONFLICT(resource_kind, resource_id, observed_at) DO UPDATE SET
  metrics_json = excluded.metrics_json`)
			if err != nil {
				return err
			}
			defer statement.Close()
			for _, sample := range samples {
				if _, err := statement.ExecContext(ctx,
					sample.Kind, sample.ResourceID, sample.ObservedAt, sample.MetricsJSON,
				); err != nil {
					return fmt.Errorf("record managed stat sample: %w", err)
				}
			}
		}
		if _, err := transaction.ExecContext(ctx,
			"DELETE FROM managed_stat_samples WHERE observed_at < ?", retentionCutoff,
		); err != nil {
			return fmt.Errorf("delete expired managed stat samples: %w", err)
		}
		return nil
	})
}

func (store *Store) ManagedStatSamples(ctx context.Context, kind, resourceID string, from, to int64) ([]ManagedStatSample, error) {
	if !validManagedStatTarget(kind, resourceID) || from <= 0 || to < from {
		return nil, errors.New("managed stat query is invalid")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT observed_at, metrics_json
FROM managed_stat_samples
WHERE resource_kind = ? AND resource_id = ? AND observed_at BETWEEN ? AND ?
ORDER BY observed_at`, kind, resourceID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query managed stat samples: %w", err)
	}
	defer rows.Close()
	samples := make([]ManagedStatSample, 0)
	for rows.Next() {
		sample := ManagedStatSample{Kind: kind, ResourceID: resourceID}
		if err := rows.Scan(&sample.ObservedAt, &sample.MetricsJSON); err != nil {
			return nil, fmt.Errorf("scan managed stat sample: %w", err)
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

func validateManagedStatSample(sample ManagedStatSample) error {
	if !validManagedStatTarget(sample.Kind, sample.ResourceID) || sample.ObservedAt <= 0 || sample.MetricsJSON == "" {
		return errors.New("managed stat sample is invalid")
	}
	return nil
}

func validManagedStatTarget(kind, resourceID string) bool {
	return (kind == "postgres" || kind == "redis" || kind == "object_store") && resourceID != ""
}
