package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrProjectChanged = errors.New("project changed")

type ProjectDeletionPlan struct {
	Project      ProjectSummary
	Services     []ServiceDesired
	Postgres     []ManagedPostgres
	Redis        []ManagedRedis
	ObjectStores []ObjectStore
	Gateways     []NetworkGateway
	Volumes      []Volume
}

type ProjectBackupResource struct {
	Kind string
	ID   string
}

type DeleteProjectInput struct {
	ID                   string
	ExpectedName         string
	DeleteBackups        bool
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

func (plan ProjectDeletionPlan) BackupResources() []ProjectBackupResource {
	resources := make([]ProjectBackupResource, 0, len(plan.Postgres)+len(plan.Redis)+len(plan.ObjectStores)+len(plan.Volumes))
	for _, resource := range plan.Postgres {
		resources = append(resources, ProjectBackupResource{Kind: "postgres", ID: resource.ID})
	}
	for _, resource := range plan.Redis {
		resources = append(resources, ProjectBackupResource{Kind: "redis", ID: resource.ID})
	}
	for _, resource := range plan.ObjectStores {
		resources = append(resources, ProjectBackupResource{Kind: "object_store", ID: resource.ID})
	}
	for _, resource := range plan.Volumes {
		resources = append(resources, ProjectBackupResource{Kind: "volume", ID: resource.ID})
	}
	return resources
}

func (store *Store) ProjectDeletionPlan(ctx context.Context, projectID string) (ProjectDeletionPlan, error) {
	project, err := store.Project(ctx, projectID)
	if err != nil {
		return ProjectDeletionPlan{}, err
	}
	plan := ProjectDeletionPlan{Project: project}
	rows, err := store.database.QueryContext(ctx, "SELECT id FROM services WHERE project_id = ? ORDER BY id", projectID)
	if err != nil {
		return ProjectDeletionPlan{}, err
	}
	serviceIDs := make([]string, 0)
	for rows.Next() {
		var serviceID string
		if err := rows.Scan(&serviceID); err != nil {
			_ = rows.Close()
			return ProjectDeletionPlan{}, err
		}
		serviceIDs = append(serviceIDs, serviceID)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return ProjectDeletionPlan{}, err
	}
	for _, serviceID := range serviceIDs {
		service, err := store.DesiredService(ctx, serviceID)
		if err != nil {
			return ProjectDeletionPlan{}, err
		}
		plan.Services = append(plan.Services, service)
	}
	if plan.Postgres, err = store.ManagedPostgresByProject(ctx, projectID); err != nil {
		return ProjectDeletionPlan{}, err
	}
	if plan.Redis, err = store.ManagedRedisByProject(ctx, projectID); err != nil {
		return ProjectDeletionPlan{}, err
	}
	if plan.ObjectStores, err = store.ObjectStoresByProject(ctx, projectID); err != nil {
		return ProjectDeletionPlan{}, err
	}
	if plan.Gateways, err = store.NetworkGateways(ctx, projectID); err != nil {
		return ProjectDeletionPlan{}, err
	}
	plan.Volumes, err = store.projectVolumes(ctx, projectID)
	if err != nil {
		return ProjectDeletionPlan{}, err
	}
	return plan, nil
}

func (store *Store) projectVolumes(ctx context.Context, projectID string) ([]Volume, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, project_id, service_id, name, created_at
FROM volumes WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Volume, 0)
	for rows.Next() {
		volume, err := scanVolume(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, volume)
	}
	return result, rows.Err()
}

func (store *Store) DeleteProject(ctx context.Context, input DeleteProjectInput) (ProjectDeletionPlan, error) {
	if input.ID == "" || input.ExpectedName == "" || input.AuditEventID == "" || input.DeletedAtMillis <= 0 ||
		validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return ProjectDeletionPlan{}, errors.New("delete project input is incomplete")
	}
	plan, err := store.ProjectDeletionPlan(ctx, input.ID)
	if err != nil {
		return ProjectDeletionPlan{}, err
	}
	if plan.Project.Name != input.ExpectedName {
		return ProjectDeletionPlan{}, ErrProjectChanged
	}
	metadata, err := json.Marshal(map[string]any{
		"actorEmail": input.ActorEmail, "projectName": plan.Project.Name,
		"deleteBackups": input.DeleteBackups, "services": len(plan.Services),
		"postgres": len(plan.Postgres), "redis": len(plan.Redis),
		"objectStores": len(plan.ObjectStores), "networkGateways": len(plan.Gateways), "volumes": len(plan.Volumes),
	})
	if err != nil {
		return ProjectDeletionPlan{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var currentName string
		if err := transaction.QueryRowContext(ctx, "SELECT name FROM projects WHERE id = ?", input.ID).Scan(&currentName); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		} else if err != nil {
			return err
		}
		if currentName != input.ExpectedName {
			return ErrProjectChanged
		}
		statements := []string{
			`DELETE FROM resource_metric_samples WHERE
			 (resource_kind = 'service' AND resource_id IN (SELECT id FROM services WHERE project_id = ?)) OR
			 (resource_kind = 'postgres' AND resource_id IN (SELECT id FROM managed_postgres WHERE project_id = ?)) OR
			 (resource_kind = 'redis' AND resource_id IN (SELECT id FROM managed_redis WHERE project_id = ?))`,
			`DELETE FROM runtime_deployments WHERE
			 (resource_kind = 'postgres' AND resource_id IN (SELECT id FROM managed_postgres WHERE project_id = ?)) OR
			 (resource_kind = 'redis' AND resource_id IN (SELECT id FROM managed_redis WHERE project_id = ?))`,
			`DELETE FROM backups WHERE
			 (resource_kind = 'postgres' AND resource_id IN (SELECT id FROM managed_postgres WHERE project_id = ?)) OR
			 (resource_kind = 'redis' AND resource_id IN (SELECT id FROM managed_redis WHERE project_id = ?)) OR
			 (resource_kind = 'object_store' AND resource_id IN (SELECT id FROM object_stores WHERE project_id = ?)) OR
			 (resource_kind = 'volume' AND resource_id IN (SELECT id FROM volumes WHERE project_id = ?))`,
			`DELETE FROM operations WHERE target_id IN (
			 SELECT id FROM services WHERE project_id = ? UNION SELECT id FROM managed_postgres WHERE project_id = ?
			 UNION SELECT id FROM managed_redis WHERE project_id = ? UNION SELECT id FROM object_stores WHERE project_id = ?
			 UNION SELECT id FROM volumes WHERE project_id = ? UNION SELECT id FROM network_gateways WHERE project_id = ?)`,
			`DELETE FROM objects WHERE object_store_id IN (SELECT id FROM object_stores WHERE project_id = ?)`,
			`DELETE FROM network_gateways WHERE project_id = ?`,
			`DELETE FROM service_secret_refs WHERE service_id IN (SELECT id FROM services WHERE project_id = ?)`,
			`DELETE FROM service_volume_mounts WHERE service_id IN (SELECT id FROM services WHERE project_id = ?)`,
			`DELETE FROM volumes WHERE project_id = ?`,
			`DELETE FROM services WHERE project_id = ?`,
		}
		arguments := [][]any{
			{input.ID, input.ID, input.ID}, {input.ID, input.ID}, {input.ID, input.ID, input.ID, input.ID},
			{input.ID, input.ID, input.ID, input.ID, input.ID, input.ID}, {input.ID}, {input.ID}, {input.ID}, {input.ID}, {input.ID}, {input.ID},
		}
		for index, statement := range statements {
			if _, err := transaction.ExecContext(ctx, statement, arguments[index]...); err != nil {
				return fmt.Errorf("delete project resources: %w", err)
			}
		}
		result, err := transaction.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", input.ID)
		if err != nil {
			return fmt.Errorf("delete project: %w", err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return errors.Join(err, ErrProjectNotFound)
		}
		var correlationID any
		if input.RequestCorrelationID != "" {
			correlationID = input.RequestCorrelationID
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(id, actor_kind, actor_id, action, target_kind, target_id,
 request_correlation_id, result, metadata_json, created_at)
VALUES (?, ?, ?, 'project.delete', 'project', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.ID, correlationID, string(metadata), input.DeletedAtMillis,
		)
		return err
	})
	return plan, err
}
