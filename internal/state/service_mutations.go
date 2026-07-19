package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
)

var (
	ErrServiceNotFound        = errors.New("service not found in project")
	ErrServiceDisabled        = errors.New("service is disabled")
	ErrDependencyMissing      = errors.New("service dependency is missing")
	ErrDeploymentNotFound     = errors.New("deployment not found for service")
	ErrDeploymentNotSuccess   = errors.New("deployment did not succeed")
	ErrDeploymentIsActive     = errors.New("deployment is active")
	ErrServiceReconcileFailed = errors.New("service reconcile failed")
	ErrPreviewDomainCount     = errors.New("PR previews require exactly one HTTP domain")
)

type UpdateServiceInput struct {
	ID                    string
	ProjectID             string
	Enabled               bool
	Snapshot              serviceconfig.Snapshot
	ImageCredential       *ServiceImageCredential
	RemoveImageCredential bool
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	UpdatedAtMillis       int64
}

type DeployServiceVersionInput struct {
	ID                    string
	ProjectID             string
	DeploymentID          string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	UpdatedAtMillis       int64
}

// RollbackServiceInput remains the automation API name. The operation creates
// a new deployment from an immutable historical version; it never rewinds
// writable volume data.
type RollbackServiceInput = DeployServiceVersionInput

type RedeployServiceInput struct {
	ID                    string
	ProjectID             string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	CreatedAtMillis       int64
}

type DeleteServiceInput struct {
	ID                    string
	ProjectID             string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	DeletedAtMillis       int64
}

type DeleteServiceResult struct {
	Service ServiceDesired
	Volumes []Volume
}

func (store *Store) DeleteService(ctx context.Context, input DeleteServiceInput) (DeleteServiceResult, error) {
	if err := validateServiceMutationIdentity(
		input.ID, input.ProjectID, input.ExpectedUpdatedMillis, input.AuditEventID,
		input.ActorKind, input.ActorID, input.ActorEmail, input.DeletedAtMillis,
	); err != nil {
		return DeleteServiceResult{}, err
	}
	service, err := store.Service(ctx, input.ProjectID, input.ID)
	if err != nil {
		return DeleteServiceResult{}, err
	}
	result := DeleteServiceResult{Service: service, Volumes: make([]Volume, 0)}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := validateServiceVersion(ctx, transaction, input.ID, input.ProjectID, input.ExpectedUpdatedMillis); err != nil {
			return err
		}
		rows, err := transaction.QueryContext(ctx, `
SELECT id, project_id, service_id, name, owner_uid, owner_gid, created_at
FROM volumes WHERE project_id = ? AND service_id = ? ORDER BY id`, input.ProjectID, input.ID)
		if err != nil {
			return fmt.Errorf("list service volumes for deletion: %w", err)
		}
		for rows.Next() {
			var volume Volume
			if err := rows.Scan(
				&volume.ID, &volume.ProjectID, &volume.ServiceID, &volume.Name,
				&volume.OwnerUID, &volume.OwnerGID, &volume.CreatedAtMillis,
			); err != nil {
				rows.Close()
				return fmt.Errorf("scan service volume for deletion: %w", err)
			}
			result.Volumes = append(result.Volumes, volume)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close service volume rows: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate service volumes for deletion: %w", err)
		}
		statements := []struct {
			query string
			args  []any
		}{
			{query: "UPDATE services SET active_deployment_id = NULL WHERE id = ?", args: []any{input.ID}},
			{query: "DELETE FROM resource_metric_samples WHERE resource_kind = 'service' AND resource_id = ?", args: []any{input.ID}},
			{query: "DELETE FROM service_volume_mounts WHERE service_id = ?", args: []any{input.ID}},
			{query: "DELETE FROM volumes WHERE project_id = ? AND service_id = ?", args: []any{input.ProjectID, input.ID}},
			{query: "DELETE FROM services WHERE id = ? AND project_id = ?", args: []any{input.ID, input.ProjectID}},
		}
		for _, statement := range statements {
			if _, err := transaction.ExecContext(ctx, statement.query, statement.args...); err != nil {
				return fmt.Errorf("delete service state: %w", err)
			}
		}
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.delete", ServiceID: input.ID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.DeletedAtMillis,
			Metadata: map[string]string{"name": service.Name, "volumeCount": fmt.Sprintf("%d", len(result.Volumes))},
		})
	})
	if err != nil {
		return DeleteServiceResult{}, err
	}
	return result, nil
}

func (store *Store) UpdateService(ctx context.Context, input UpdateServiceInput) (ServiceDesired, error) {
	if err := validateServiceMutationIdentity(input.ID, input.ProjectID, input.ExpectedUpdatedMillis, input.AuditEventID, input.ActorKind, input.ActorID, input.ActorEmail, input.UpdatedAtMillis); err != nil {
		return ServiceDesired{}, err
	}
	snapshot, err := serviceconfig.Normalize(input.Snapshot)
	if err != nil {
		return ServiceDesired{}, err
	}
	updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := validateServiceVersion(ctx, transaction, input.ID, input.ProjectID, input.ExpectedUpdatedMillis); err != nil {
			return err
		}
		if input.ImageCredential != nil {
			if input.ImageCredential.ServiceID != input.ID {
				return errors.New("service image credential belongs to another service")
			}
			if err := replaceServiceImageCredential(ctx, transaction, *input.ImageCredential); err != nil {
				return err
			}
		} else if input.RemoveImageCredential {
			if _, err := transaction.ExecContext(ctx, "DELETE FROM service_image_credentials WHERE service_id = ?", input.ID); err != nil {
				return fmt.Errorf("remove service image credential: %w", err)
			}
		}
		if err := validateServiceDependencies(ctx, transaction, input.ProjectID, input.ID, snapshot); err != nil {
			return err
		}
		if err := replaceServiceConfig(ctx, transaction, input.ID, input.ProjectID, snapshot, input.Enabled, input.ExpectedUpdatedMillis, updatedAt); err != nil {
			return err
		}
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.update", ServiceID: input.ID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: updatedAt,
		})
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.DesiredService(ctx, input.ID)
}

func (store *Store) DeployServiceVersion(ctx context.Context, input DeployServiceVersionInput) (ServiceDesired, error) {
	if input.DeploymentID == "" {
		return ServiceDesired{}, errors.New("deployment version ID is empty")
	}
	if err := validateServiceMutationIdentity(input.ID, input.ProjectID, input.ExpectedUpdatedMillis, input.AuditEventID, input.ActorKind, input.ActorID, input.ActorEmail, input.UpdatedAtMillis); err != nil {
		return ServiceDesired{}, err
	}
	updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var imageDigest string
		var snapshotJSON string
		var status string
		var sourceRevision sql.NullString
		var enabled int
		var currentUpdated int64
		err := transaction.QueryRowContext(ctx, `
SELECT d.image_digest, d.source_revision, d.snapshot_json, d.status, s.enabled, s.updated_at
FROM services s
JOIN deployments d ON d.service_id = s.id
WHERE s.id = ? AND s.project_id = ? AND d.id = ?`, input.ID, input.ProjectID, input.DeploymentID).Scan(
			&imageDigest, &sourceRevision, &snapshotJSON, &status, &enabled, &currentUpdated,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeploymentNotFound
		}
		if err != nil {
			return fmt.Errorf("load deployment version: %w", err)
		}
		if currentUpdated != input.ExpectedUpdatedMillis {
			return ErrServiceChanged
		}
		if status != "succeeded" && status != "failed" && status != "skipped" {
			return ErrDeploymentNotSuccess
		}
		var snapshot serviceconfig.Snapshot
		if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
			return fmt.Errorf("decode deployment version snapshot: %w", err)
		}
		if servicesource.IsImage(snapshot.Source) {
			pinned, err := serviceconfig.PinnedReference(servicesource.ImageReference(snapshot.Source), imageDigest)
			if err != nil {
				return err
			}
			snapshot.Source.Image.Reference = pinned
			snapshot.Source.AutoUpdate = false
		} else if snapshot.Source.Type == servicesource.GitHubImage {
			if !sourceRevision.Valid {
				return errors.New("GitHub deployment has no source revision")
			}
		}
		snapshot, err = serviceconfig.Normalize(snapshot)
		if err != nil {
			return err
		}
		if err := validateServiceDependencies(ctx, transaction, input.ProjectID, input.ID, snapshot); err != nil {
			return err
		}
		if err := replaceServiceConfig(ctx, transaction, input.ID, input.ProjectID, snapshot, enabled == 1, input.ExpectedUpdatedMillis, updatedAt); err != nil {
			return err
		}
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.deploy_version", ServiceID: input.ID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: updatedAt,
			Metadata: map[string]string{"deploymentId": input.DeploymentID},
		})
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.DesiredService(ctx, input.ID)
}

// RollbackService is the token automation surface's historical name for
// deploying an immutable version. The admin UI and state model use the more
// precise deploy-version terminology.
func (store *Store) RollbackService(ctx context.Context, input RollbackServiceInput) (ServiceDesired, error) {
	return store.DeployServiceVersion(ctx, input)
}

type DeleteServiceDeploymentInput struct {
	ID                    string
	ProjectID             string
	DeploymentID          string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	CreatedAtMillis       int64
}

func (store *Store) DeleteServiceDeployment(ctx context.Context, input DeleteServiceDeploymentInput) error {
	if input.DeploymentID == "" {
		return ErrDeploymentNotFound
	}
	if err := validateServiceMutationIdentity(input.ID, input.ProjectID, input.ExpectedUpdatedMillis, input.AuditEventID, input.ActorKind, input.ActorID, input.ActorEmail, input.CreatedAtMillis); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var activeDeploymentID sql.NullString
		if err := transaction.QueryRowContext(ctx, `
SELECT active_deployment_id FROM services
WHERE id = ? AND project_id = ? AND updated_at = ?`, input.ID, input.ProjectID, input.ExpectedUpdatedMillis).Scan(&activeDeploymentID); errors.Is(err, sql.ErrNoRows) {
			return ErrServiceChanged
		} else if err != nil {
			return err
		}
		if activeDeploymentID.String == input.DeploymentID {
			return ErrDeploymentIsActive
		}
		result, err := transaction.ExecContext(ctx, "DELETE FROM deployments WHERE id = ? AND service_id = ?", input.DeploymentID, input.ID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrDeploymentNotFound
		}
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.deployment_remove", ServiceID: input.ID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.CreatedAtMillis,
			Metadata: map[string]string{"deploymentId": input.DeploymentID},
		})
	})
}

func (store *Store) RedeployService(ctx context.Context, input RedeployServiceInput) (ServiceDesired, error) {
	if err := validateServiceMutationIdentity(input.ID, input.ProjectID, input.ExpectedUpdatedMillis, input.AuditEventID, input.ActorKind, input.ActorID, input.ActorEmail, input.CreatedAtMillis); err != nil {
		return ServiceDesired{}, err
	}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var enabled int
		var updatedAt int64
		err := transaction.QueryRowContext(ctx, `
SELECT enabled, updated_at FROM services WHERE id = ? AND project_id = ?`, input.ID, input.ProjectID).Scan(&enabled, &updatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrServiceNotFound
		}
		if err != nil {
			return fmt.Errorf("load redeploy service: %w", err)
		}
		if updatedAt != input.ExpectedUpdatedMillis {
			return ErrServiceChanged
		}
		if enabled != 1 {
			return ErrServiceDisabled
		}
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.redeploy", ServiceID: input.ID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.CreatedAtMillis,
		})
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.DesiredService(ctx, input.ID)
}
