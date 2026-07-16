package state

import (
	"context"
	"database/sql"
	"fmt"
)

type ProjectResource struct {
	ID   string
	Kind string
	Name string
}

func (store *Store) ProjectResources(ctx context.Context, projectID string) ([]ProjectResource, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, kind, name FROM (
  SELECT id, 'service' AS kind, name FROM services WHERE project_id = ?
  UNION ALL
  SELECT id, 'postgres', name FROM managed_postgres WHERE project_id = ?
  UNION ALL
  SELECT id, 'redis', name FROM managed_redis WHERE project_id = ?
  UNION ALL
  SELECT id, 'object_store', name FROM object_stores WHERE project_id = ?
) ORDER BY name, kind, id`, projectID, projectID, projectID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project resources: %w", err)
	}
	defer rows.Close()
	resources := make([]ProjectResource, 0)
	for rows.Next() {
		var resource ProjectResource
		if err := rows.Scan(&resource.ID, &resource.Kind, &resource.Name); err != nil {
			return nil, fmt.Errorf("scan project resource: %w", err)
		}
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project resources: %w", err)
	}
	return resources, nil
}

func (store *Store) ProjectResourceByName(ctx context.Context, projectID, name string) (ProjectResource, error) {
	var resource ProjectResource
	err := store.database.QueryRowContext(ctx, `
SELECT id, kind, name FROM (
  SELECT id, 'service' AS kind, name FROM services WHERE project_id = ?
  UNION ALL
  SELECT id, 'postgres', name FROM managed_postgres WHERE project_id = ?
  UNION ALL
  SELECT id, 'redis', name FROM managed_redis WHERE project_id = ?
  UNION ALL
  SELECT id, 'object_store', name FROM object_stores WHERE project_id = ?
) WHERE name = ?`, projectID, projectID, projectID, projectID, name).Scan(
		&resource.ID, &resource.Kind, &resource.Name,
	)
	if err == sql.ErrNoRows {
		return ProjectResource{}, sql.ErrNoRows
	}
	if err != nil {
		return ProjectResource{}, fmt.Errorf("load project resource %q: %w", name, err)
	}
	return resource, nil
}
