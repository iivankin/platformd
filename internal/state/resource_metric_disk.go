package state

import (
	"context"
	"database/sql"
	"fmt"
)

type ResourceMetricDiskTarget struct {
	Kind       string
	ResourceID string
	ProjectID  string
	VolumeIDs  []string
}

func (store *Store) ResourceMetricDiskTargets(ctx context.Context) ([]ResourceMetricDiskTarget, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT kind, resource_id, project_id, volume_id FROM (
  SELECT 'service' AS kind, s.id AS resource_id, s.project_id, v.id AS volume_id
  FROM services s LEFT JOIN volumes v ON v.service_id = s.id
  UNION ALL
  SELECT 'postgres', id, project_id, volume_id FROM managed_postgres
  UNION ALL
  SELECT 'redis', id, project_id, volume_id FROM managed_redis
) ORDER BY kind, resource_id, volume_id`)
	if err != nil {
		return nil, fmt.Errorf("list resource metric disk targets: %w", err)
	}
	defer rows.Close()

	targets := make([]ResourceMetricDiskTarget, 0)
	for rows.Next() {
		var kind, resourceID, projectID string
		var volumeID sql.NullString
		if err := rows.Scan(&kind, &resourceID, &projectID, &volumeID); err != nil {
			return nil, fmt.Errorf("scan resource metric disk target: %w", err)
		}
		if len(targets) == 0 || targets[len(targets)-1].Kind != kind || targets[len(targets)-1].ResourceID != resourceID {
			targets = append(targets, ResourceMetricDiskTarget{
				Kind: kind, ResourceID: resourceID, ProjectID: projectID,
			})
		}
		if volumeID.Valid {
			targets[len(targets)-1].VolumeIDs = append(targets[len(targets)-1].VolumeIDs, volumeID.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource metric disk targets: %w", err)
	}
	return targets, nil
}
