package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/iivankin/platformd/internal/variableexpression"
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
	Volumes          []CanvasVolume
}

type CanvasVolume struct {
	ID            string
	Name          string
	ContainerPath string
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
		resource.Volumes = make([]CanvasVolume, 0)
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project canvas resources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close project canvas resources: %w", err)
	}
	resourceIndex := make(map[string]int, len(resources))
	for index := range resources {
		resourceIndex[resources[index].ID] = index
	}
	volumeRows, err := store.database.QueryContext(ctx, `
SELECT v.id, v.service_id, v.name, m.container_path
FROM volumes v
LEFT JOIN service_volume_mounts m ON m.volume_id = v.id
WHERE v.project_id = ?
ORDER BY v.service_id, v.name, v.id`, project.ID)
	if err != nil {
		return nil, fmt.Errorf("list project canvas volumes: %w", err)
	}
	defer volumeRows.Close()
	for volumeRows.Next() {
		var volume CanvasVolume
		var serviceID string
		var containerPath sql.NullString
		if err := volumeRows.Scan(&volume.ID, &serviceID, &volume.Name, &containerPath); err != nil {
			return nil, fmt.Errorf("scan project canvas volume: %w", err)
		}
		index, ok := resourceIndex[serviceID]
		if !ok {
			continue
		}
		volume.ContainerPath = containerPath.String
		resources[index].Volumes = append(resources[index].Volumes, volume)
	}
	if err := volumeRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project canvas volumes: %w", err)
	}
	return resources, nil
}

func (store *Store) canvasConnections(ctx context.Context, projectID string) ([]CanvasConnection, error) {
	resources, err := store.ProjectResources(ctx, projectID)
	if err != nil {
		return nil, err
	}
	resourceIDs := make(map[string]string, len(resources))
	for _, resource := range resources {
		resourceIDs[resource.Name] = resource.ID
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, environment_json FROM services
WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project canvas connections: %w", err)
	}
	defer rows.Close()
	type connectionKey struct{ sourceID, targetID string }
	connections := make(map[connectionKey]map[string]struct{})
	for rows.Next() {
		var sourceID, environmentJSON string
		if err := rows.Scan(&sourceID, &environmentJSON); err != nil {
			return nil, fmt.Errorf("scan project canvas connection: %w", err)
		}
		var environment map[string]string
		if err := json.Unmarshal([]byte(environmentJSON), &environment); err != nil {
			return nil, fmt.Errorf("decode project service environment: %w", err)
		}
		for environmentName, value := range environment {
			references, parseErr := variableexpression.References(value)
			if parseErr != nil {
				continue
			}
			for _, reference := range references {
				targetID := resourceIDs[reference.Resource]
				if targetID == "" {
					continue
				}
				key := connectionKey{sourceID: sourceID, targetID: targetID}
				if connections[key] == nil {
					connections[key] = make(map[string]struct{})
				}
				connections[key][environmentName] = struct{}{}
			}
		}
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
