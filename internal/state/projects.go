package state

import (
	"context"
	"fmt"
)

type RuntimeProject struct {
	ID                 string
	Name               string
	ObjectStoreEnabled bool
}

func (store *Store) RuntimeProjects(ctx context.Context) ([]RuntimeProject, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT p.id, p.name,
       EXISTS(SELECT 1 FROM object_stores o WHERE o.project_id = p.id)
FROM projects p
ORDER BY p.id`)
	if err != nil {
		return nil, fmt.Errorf("list runtime projects: %w", err)
	}
	defer rows.Close()
	var result []RuntimeProject
	for rows.Next() {
		var project RuntimeProject
		var objectStoreEnabled int
		if err := rows.Scan(&project.ID, &project.Name, &objectStoreEnabled); err != nil {
			return nil, fmt.Errorf("scan runtime project: %w", err)
		}
		project.ObjectStoreEnabled = objectStoreEnabled == 1
		result = append(result, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime projects: %w", err)
	}
	return result, nil
}
