package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

var ErrProjectNotFound = errors.New("project not found")

type CanvasResource struct {
	ID               string
	Kind             string
	Name             string
	InternalHostname string
	ImageReference   string
	BucketName       string
	Enabled          bool
	Status           string
	StatusMessage    string
	ActiveDeployment string
	ImageDigest      string
}

type CanvasConnection struct {
	SourceID         string
	TargetID         string
	EnvironmentNames []string
}

type ProjectCanvas struct {
	Project     ProjectSummary
	Resources   []CanvasResource
	Connections []CanvasConnection
}

func (store *Store) ProjectCanvas(ctx context.Context, projectID string) (ProjectCanvas, error) {
	project, err := store.Project(ctx, projectID)
	if err != nil {
		return ProjectCanvas{}, err
	}
	resources, err := store.canvasResources(ctx, project)
	if err != nil {
		return ProjectCanvas{}, err
	}
	connections, err := store.canvasConnections(ctx, projectID)
	if err != nil {
		return ProjectCanvas{}, err
	}
	return ProjectCanvas{
		Project:     project,
		Resources:   resources,
		Connections: connections,
	}, nil
}

func (store *Store) Project(ctx context.Context, projectID string) (ProjectSummary, error) {
	var project ProjectSummary
	err := store.database.QueryRowContext(ctx, `
SELECT p.id, p.name,
       (SELECT count(*) FROM services s WHERE s.project_id = p.id),
       (SELECT count(*) FROM managed_postgres pg WHERE pg.project_id = p.id),
       (SELECT count(*) FROM managed_redis r WHERE r.project_id = p.id),
       (SELECT count(*) FROM object_stores o WHERE o.project_id = p.id),
       p.created_at, p.updated_at
FROM projects p
WHERE p.id = ?`, projectID).Scan(
		&project.ID, &project.Name, &project.ServiceCount,
		&project.PostgresCount, &project.RedisCount, &project.ObjectStoreCount,
		&project.CreatedAtMillis, &project.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectSummary{}, ErrProjectNotFound
	}
	if err != nil {
		return ProjectSummary{}, fmt.Errorf("load project canvas project: %w", err)
	}
	return project, nil
}

func (store *Store) canvasResources(ctx context.Context, project ProjectSummary) ([]CanvasResource, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, kind, name, image_reference, bucket_name, enabled,
       active_deployment_id, image_digest, status, status_message
FROM (
  SELECT s.id, 'service' AS kind, s.name, s.image_reference, '' AS bucket_name,
         s.enabled, COALESCE(s.active_deployment_id, '') AS active_deployment_id,
         COALESCE(d.image_digest, '') AS image_digest,
         CASE
           WHEN s.enabled = 0 THEN 'disabled'
           WHEN latest.status IN ('failed', 'interrupted') AND s.active_deployment_id IS NULL THEN 'failed'
           WHEN latest.status IN ('failed', 'interrupted') THEN 'degraded'
           ELSE 'pending'
         END AS status,
         CASE WHEN latest.status IN ('failed', 'interrupted')
              THEN COALESCE(latest.error_message, latest.status)
              ELSE '' END AS status_message
  FROM services s
  LEFT JOIN deployments d ON d.id = s.active_deployment_id
  LEFT JOIN deployments latest ON latest.id = (
    SELECT candidate.id FROM deployments candidate
    WHERE candidate.service_id = s.id
    ORDER BY candidate.created_at DESC, candidate.id DESC LIMIT 1
  )
  WHERE s.project_id = ?
  UNION ALL
  SELECT id, 'postgres' AS kind, name, image_tag AS image_reference,
         '' AS bucket_name, 1 AS enabled,
         '', image_digest, 'pending', ''
  FROM managed_postgres WHERE project_id = ?
  UNION ALL
  SELECT id, 'redis' AS kind, name, image_tag AS image_reference,
         '' AS bucket_name, 1 AS enabled,
         '', image_digest, 'pending', ''
  FROM managed_redis WHERE project_id = ?
  UNION ALL
  SELECT id, 'object_store' AS kind, name, '' AS image_reference,
         bucket_name, 1 AS enabled,
         '', '', 'pending', ''
  FROM object_stores WHERE project_id = ?
)
ORDER BY kind, name, id`, project.ID, project.ID, project.ID, project.ID)
	if err != nil {
		return nil, fmt.Errorf("list project canvas resources: %w", err)
	}
	defer rows.Close()

	resources := make([]CanvasResource, 0)
	for rows.Next() {
		var resource CanvasResource
		var enabled int
		if err := rows.Scan(
			&resource.ID, &resource.Kind, &resource.Name, &resource.ImageReference,
			&resource.BucketName, &enabled,
			&resource.ActiveDeployment, &resource.ImageDigest, &resource.Status,
			&resource.StatusMessage,
		); err != nil {
			return nil, fmt.Errorf("scan project canvas resource: %w", err)
		}
		resource.Enabled = enabled == 1
		resource.InternalHostname = resource.Name + "." + project.Name + ".internal"
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project canvas resources: %w", err)
	}
	return resources, nil
}

func (store *Store) canvasConnections(ctx context.Context, projectID string) ([]CanvasConnection, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT refs.service_id, refs.resource_id, refs.environment_name
FROM service_resource_variable_refs refs
JOIN services source ON source.id = refs.service_id
WHERE source.project_id = ?
ORDER BY refs.service_id, refs.resource_id, refs.environment_name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project canvas connections: %w", err)
	}
	defer rows.Close()
	type connectionKey struct{ sourceID, targetID string }
	connections := make(map[connectionKey]map[string]struct{})
	for rows.Next() {
		var sourceID, targetID, environmentName string
		if err := rows.Scan(&sourceID, &targetID, &environmentName); err != nil {
			return nil, fmt.Errorf("scan project canvas connection: %w", err)
		}
		key := connectionKey{sourceID: sourceID, targetID: targetID}
		if connections[key] == nil {
			connections[key] = make(map[string]struct{})
		}
		connections[key][environmentName] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project canvas connections: %w", err)
	}

	result := make([]CanvasConnection, 0, len(connections))
	for key, environmentNames := range connections {
		names := make([]string, 0, len(environmentNames))
		for name := range environmentNames {
			names = append(names, name)
		}
		sort.Strings(names)
		result = append(result, CanvasConnection{
			SourceID: key.sourceID, TargetID: key.targetID, EnvironmentNames: names,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].SourceID == result[right].SourceID {
			return result[left].TargetID < result[right].TargetID
		}
		return result[left].SourceID < result[right].SourceID
	})
	return result, nil
}
