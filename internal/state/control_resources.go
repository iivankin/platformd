package state

import (
	"context"
	"fmt"
)

type ControlResourceIDs struct {
	Images       []string `json:"images"`
	ObjectStores []string `json:"objectStores"`
	Postgres     []string `json:"postgres"`
	Redis        []string `json:"redis"`
	Volumes      []string `json:"volumes"`
}

func (store *Store) ControlResources(ctx context.Context) (ControlResourceIDs, error) {
	result := ControlResourceIDs{}
	queries := []struct {
		target *[]string
		query  string
	}{
		{&result.Images, `SELECT r.id FROM service_image_revisions r
WHERE r.status = 'active' AND (
  (r.kind = 'production' AND EXISTS(
    SELECT 1 FROM services s WHERE s.id = r.service_id AND s.enabled = 1 AND s.active_deployment_id = r.deployment_id
  )) OR
  (r.kind = 'preview' AND EXISTS(
    SELECT 1 FROM preview_deployments p WHERE p.id = r.preview_id AND p.status = 'active'
  ))
) ORDER BY r.id`},
		{&result.ObjectStores, "SELECT id FROM object_stores ORDER BY id"},
		{&result.Postgres, "SELECT id FROM managed_postgres ORDER BY id"},
		{&result.Redis, "SELECT id FROM managed_redis ORDER BY id"},
		{&result.Volumes, "SELECT id FROM volumes ORDER BY id"},
	}
	for _, item := range queries {
		rows, err := store.database.QueryContext(ctx, item.query)
		if err != nil {
			return ControlResourceIDs{}, err
		}
		for rows.Next() {
			var identifier string
			if err := rows.Scan(&identifier); err != nil {
				_ = rows.Close()
				return ControlResourceIDs{}, err
			}
			*item.target = append(*item.target, identifier)
		}
		if err := rows.Close(); err != nil {
			return ControlResourceIDs{}, fmt.Errorf("close control resource query: %w", err)
		}
	}
	return result, nil
}
