package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/resourcename"
)

var ErrProjectNameConflict = errors.New("project name already exists")

type ProjectSummary struct {
	ID               string
	Name             string
	ServiceCount     int
	PostgresCount    int
	RedisCount       int
	ObjectStoreCount int
	CreatedAtMillis  int64
	UpdatedAtMillis  int64
}

type CreateProject struct {
	ID                   string
	Name                 string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) Projects(ctx context.Context) ([]ProjectSummary, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT p.id, p.name,
       (SELECT count(*) FROM services s WHERE s.project_id = p.id),
       (SELECT count(*) FROM managed_postgres pg WHERE pg.project_id = p.id),
       (SELECT count(*) FROM managed_redis r WHERE r.project_id = p.id),
       (SELECT count(*) FROM object_stores o WHERE o.project_id = p.id),
       p.created_at, p.updated_at
FROM projects p
ORDER BY p.name, p.id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var result []ProjectSummary
	for rows.Next() {
		var project ProjectSummary
		if err := rows.Scan(
			&project.ID, &project.Name, &project.ServiceCount,
			&project.PostgresCount, &project.RedisCount, &project.ObjectStoreCount,
			&project.CreatedAtMillis, &project.UpdatedAtMillis,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		result = append(result, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return result, nil
}

func (store *Store) CreateProject(ctx context.Context, input CreateProject) (ProjectSummary, error) {
	if input.ID == "" || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || input.CreatedAtMillis <= 0 {
		return ProjectSummary{}, errors.New("create project input is incomplete")
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return ProjectSummary{}, err
	}
	metadata, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail})
	if err != nil {
		return ProjectSummary{}, err
	}
	err = store.Write(ctx, func(transaction *sql.Tx) error {
		var existingID string
		err := transaction.QueryRowContext(ctx, "SELECT id FROM projects WHERE name = ?", input.Name).Scan(&existingID)
		if err == nil {
			return ErrProjectNameConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check project name: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at)
VALUES (?, ?, ?, ?)`, input.ID, input.Name, input.CreatedAtMillis, input.CreatedAtMillis); err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		var correlationID any
		if input.RequestCorrelationID != "" {
			correlationID = input.RequestCorrelationID
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'project.create', 'project', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, input.ID, correlationID, string(metadata), input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("audit project creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return ProjectSummary{}, err
	}
	return ProjectSummary{
		ID: input.ID, Name: input.Name,
		CreatedAtMillis: input.CreatedAtMillis, UpdatedAtMillis: input.CreatedAtMillis,
	}, nil
}
