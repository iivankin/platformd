package state

import (
	"context"
	"fmt"
)

func (store *Store) ReferencedContainerImageDigests(ctx context.Context) (map[string]struct{}, error) {
	return store.containerImageDigests(ctx, `
SELECT deployments.image_digest
FROM services
JOIN deployments ON deployments.id = services.active_deployment_id
UNION
SELECT image_digest FROM managed_postgres
UNION
SELECT image_digest FROM managed_redis
UNION
SELECT image_digest FROM preview_deployments WHERE status = 'active'`,
		"referenced",
	)
}

func (store *Store) KnownContainerImageDigests(ctx context.Context) (map[string]struct{}, error) {
	return store.containerImageDigests(ctx, `
SELECT image_digest FROM deployments
UNION
SELECT image_digest FROM preview_deployments WHERE image_revision_id IS NOT NULL
UNION
SELECT image_digest FROM runtime_deployments
UNION
SELECT image_digest FROM managed_postgres
UNION
SELECT image_digest FROM managed_redis`,
		"known",
	)
}

func (store *Store) containerImageDigests(ctx context.Context, query, description string) (map[string]struct{}, error) {
	rows, err := store.database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list %s container image digests: %w", description, err)
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, fmt.Errorf("scan %s container image digest: %w", description, err)
		}
		if digest != "" {
			result[digest] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s container image digests: %w", description, err)
	}
	return result, nil
}
