package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	Environment      map[string]string
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
	return ProjectCanvas{
		Project:     project,
		Resources:   resources,
		Connections: deriveCanvasConnections(resources),
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
SELECT id, kind, name, image_reference, bucket_name, enabled, environment_json,
       active_deployment_id, image_digest, status, status_message
FROM (
  SELECT s.id, 'service' AS kind, s.name, s.image_reference, '' AS bucket_name,
         s.enabled, s.environment_json, COALESCE(s.active_deployment_id, '') AS active_deployment_id,
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
         '' AS bucket_name, 1 AS enabled, '{}' AS environment_json,
         '', image_digest, 'pending', ''
  FROM managed_postgres WHERE project_id = ?
  UNION ALL
  SELECT id, 'redis' AS kind, name, image_tag AS image_reference,
         '' AS bucket_name, 1 AS enabled, '{}' AS environment_json,
         '', image_digest, 'pending', ''
  FROM managed_redis WHERE project_id = ?
  UNION ALL
  SELECT id, 'object_store' AS kind, name, '' AS image_reference,
         bucket_name, 1 AS enabled, '{}' AS environment_json,
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
		var environmentJSON string
		if err := rows.Scan(
			&resource.ID, &resource.Kind, &resource.Name, &resource.ImageReference,
			&resource.BucketName, &enabled, &environmentJSON,
			&resource.ActiveDeployment, &resource.ImageDigest, &resource.Status,
			&resource.StatusMessage,
		); err != nil {
			return nil, fmt.Errorf("scan project canvas resource: %w", err)
		}
		resource.Enabled = enabled == 1
		resource.InternalHostname = resource.Name + "." + project.Name + ".internal"
		if resource.Kind == "service" {
			if err := json.Unmarshal([]byte(environmentJSON), &resource.Environment); err != nil {
				return nil, fmt.Errorf("decode service %s environment: %w", resource.ID, err)
			}
		}
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project canvas resources: %w", err)
	}
	return resources, nil
}

func deriveCanvasConnections(resources []CanvasResource) []CanvasConnection {
	targets := make(map[string]CanvasResource, len(resources)*2)
	for _, resource := range resources {
		targets[resource.Name] = resource
		targets[resource.InternalHostname] = resource
	}

	type connectionKey struct{ sourceID, targetID string }
	connections := make(map[connectionKey]map[string]struct{})
	for _, source := range resources {
		if source.Kind != "service" {
			continue
		}
		for environmentName, value := range source.Environment {
			for _, hostname := range hostnameTokens(value) {
				target, ok := targets[hostname]
				if !ok || target.ID == source.ID {
					continue
				}
				key := connectionKey{sourceID: source.ID, targetID: target.ID}
				if connections[key] == nil {
					connections[key] = make(map[string]struct{})
				}
				connections[key][environmentName] = struct{}{}
			}
		}
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
	return result
}

func hostnameTokens(value string) []string {
	lower := strings.ToLower(value)
	result := make([]string, 0)
	for index := 0; index < len(lower); {
		for index < len(lower) && !hostnameByte(lower[index]) {
			index++
		}
		start := index
		for index < len(lower) && hostnameByte(lower[index]) {
			index++
		}
		if start == index || strings.HasPrefix(lower[index:], "://") {
			continue
		}
		token := strings.Trim(lower[start:index], ".")
		if token != "" {
			result = append(result, token)
		}
	}
	return result
}

func hostnameByte(value byte) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= '0' && value <= '9') || value == '-' || value == '.'
}
