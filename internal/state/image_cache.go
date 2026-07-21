package state

import (
	"context"
	"fmt"
)

func (store *Store) ReferencedContainerImageDigests(ctx context.Context) (map[string]struct{}, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT deployments.image_digest
FROM services
JOIN deployments ON deployments.id = services.active_deployment_id
UNION
SELECT image_digest FROM managed_postgres
UNION
SELECT image_digest FROM managed_redis
UNION
SELECT image_digest FROM preview_deployments WHERE status = 'active'`)

	if err != nil {
		return nil, fmt.Errorf("list referenced container image digests: %w", err)
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, fmt.Errorf("scan referenced container image digest: %w", err)
		}
		if digest != "" {
			result[digest] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate referenced container image digests: %w", err)
	}
	return result, nil
}
