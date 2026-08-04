package state

import (
	"context"
	"errors"
	"fmt"
)

type ResourceMetricSeries struct {
	Kind       string
	ResourceID string
	Name       string
	Samples    []ResourceMetricSample
}

type AggregateMetricSeries struct {
	ScopeID string
	Name    string
	Samples []AggregateMetricSample
}

func (store *Store) ResourceMetricSeriesByProject(ctx context.Context, projectID string, from, to int64) ([]ResourceMetricSeries, error) {
	if projectID == "" || from <= 0 || to < from {
		return nil, errors.New("project resource metric series query is invalid")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT m.observed_at, m.duration_millis,
       m.cpu_duration_millis, m.network_duration_millis, m.proxy_duration_millis,
       m.cpu_millicores, m.cpu_peak_millicores,
       m.memory_bytes, m.memory_peak_bytes, m.disk_bytes,
       m.network_ingress_bytes_per_second, m.network_ingress_peak_bytes_per_second,
       m.network_egress_bytes_per_second, m.network_egress_peak_bytes_per_second,
       m.running, m.proxy_metrics_json,
       resources.kind, resources.id, resources.name
FROM (
  SELECT 'service' AS kind, id, project_id, name FROM services
  UNION ALL SELECT 'postgres', id, project_id, name FROM managed_postgres
  UNION ALL SELECT 'redis', id, project_id, name FROM managed_redis
) resources
JOIN resource_metric_samples m
  ON m.resource_kind = resources.kind AND m.resource_id = resources.id
WHERE resources.project_id = ? AND m.observed_at BETWEEN ? AND ?
ORDER BY resources.name, resources.kind, resources.id, m.observed_at`, projectID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query project resource metric series: %w", err)
	}
	defer rows.Close()

	series := make([]ResourceMetricSeries, 0)
	for rows.Next() {
		var kind, resourceID, name string
		sample, err := scanResourceMetricSample(rows, "", "", &kind, &resourceID, &name)
		if err != nil {
			return nil, fmt.Errorf("scan project resource metric series: %w", err)
		}
		sample.Kind, sample.ResourceID = kind, resourceID
		if len(series) == 0 || series[len(series)-1].Kind != kind || series[len(series)-1].ResourceID != resourceID {
			series = append(series, ResourceMetricSeries{Kind: kind, ResourceID: resourceID, Name: name})
		}
		series[len(series)-1].Samples = append(series[len(series)-1].Samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project resource metric series: %w", err)
	}
	return series, nil
}

func (store *Store) ProjectAggregateMetricSeries(ctx context.Context, from, to int64) ([]AggregateMetricSeries, error) {
	if from <= 0 || to < from {
		return nil, errors.New("project aggregate metric series query is invalid")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT m.observed_at, m.cpu_millicores, m.memory_bytes,
       m.duration_millis, m.cpu_duration_millis, m.network_duration_millis, m.proxy_duration_millis,
       m.cpu_peak_millicores, m.memory_peak_bytes, m.disk_bytes,
       m.network_ingress_bytes_per_second, m.network_ingress_peak_bytes_per_second,
       m.network_egress_bytes_per_second, m.network_egress_peak_bytes_per_second,
       m.running_resources, m.total_resources, m.proxy_metrics_json,
       projects.id, projects.name
FROM projects
JOIN aggregate_metric_samples m ON m.scope_kind = 'project' AND m.scope_id = projects.id
WHERE m.observed_at BETWEEN ? AND ?
ORDER BY projects.name, projects.id, m.observed_at`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query project aggregate metric series: %w", err)
	}
	defer rows.Close()

	series := make([]AggregateMetricSeries, 0)
	for rows.Next() {
		var projectID, name string
		sample, err := scanAggregateMetricSample(rows, "project", "", &projectID, &name)
		if err != nil {
			return nil, fmt.Errorf("scan project aggregate metric series: %w", err)
		}
		sample.ScopeID = projectID
		if len(series) == 0 || series[len(series)-1].ScopeID != projectID {
			series = append(series, AggregateMetricSeries{ScopeID: projectID, Name: name})
		}
		series[len(series)-1].Samples = append(series[len(series)-1].Samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project aggregate metric series: %w", err)
	}
	return series, nil
}
