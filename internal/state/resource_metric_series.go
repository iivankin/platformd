package state

import (
	"context"
	"errors"
	"fmt"
)

type MetricCatalogResource struct {
	Kind string
	ID   string
	Name string
}

type MetricCatalogProject struct {
	ID   string
	Name string
}

func (store *Store) ResourceMetricCatalog(ctx context.Context, projectID string) ([]MetricCatalogResource, error) {
	if projectID == "" {
		return nil, errors.New("metric catalog project is invalid")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT kind, id, name FROM (
  SELECT 'service' AS kind, id, project_id, name FROM services
  UNION ALL SELECT 'postgres', id, project_id, name FROM managed_postgres
  UNION ALL SELECT 'redis', id, project_id, name FROM managed_redis
  UNION ALL SELECT 'network_gateway', id, project_id, name FROM network_gateways
) WHERE project_id = ? ORDER BY name, kind, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query project metric catalog: %w", err)
	}
	defer rows.Close()
	resources := make([]MetricCatalogResource, 0)
	for rows.Next() {
		var resource MetricCatalogResource
		if err := rows.Scan(&resource.Kind, &resource.ID, &resource.Name); err != nil {
			return nil, fmt.Errorf("scan project metric catalog: %w", err)
		}
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func (store *Store) ProjectMetricCatalog(ctx context.Context) ([]MetricCatalogProject, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT id, name FROM projects ORDER BY name, id")
	if err != nil {
		return nil, fmt.Errorf("query project metric catalog: %w", err)
	}
	defer rows.Close()
	projects := make([]MetricCatalogProject, 0)
	for rows.Next() {
		var project MetricCatalogProject
		if err := rows.Scan(&project.ID, &project.Name); err != nil {
			return nil, fmt.Errorf("scan project metric catalog: %w", err)
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

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
