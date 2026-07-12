package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

var (
	ErrServiceNotFound        = errors.New("service not found in project")
	ErrServiceDisabled        = errors.New("service is disabled")
	ErrDependencyMissing      = errors.New("service dependency is missing")
	ErrDeploymentNotFound     = errors.New("deployment not found for service")
	ErrDeploymentNotSuccess   = errors.New("deployment did not succeed")
	ErrServiceReconcileFailed = errors.New("service reconcile failed")
)

type UpdateServiceInput struct {
	ID                    string
	ProjectID             string
	Enabled               bool
	Snapshot              serviceconfig.Snapshot
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	UpdatedAtMillis       int64
}

type RollbackServiceInput struct {
	ID                    string
	ProjectID             string
	DeploymentID          string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	UpdatedAtMillis       int64
}

type RedeployServiceInput struct {
	ID                    string
	ProjectID             string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	CreatedAtMillis       int64
}

func (store *Store) UpdateService(ctx context.Context, input UpdateServiceInput) (ServiceDesired, error) {
	if err := validateServiceMutationIdentity(input.ID, input.ProjectID, input.ExpectedUpdatedMillis, input.AuditEventID, input.ActorID, input.ActorEmail, input.UpdatedAtMillis); err != nil {
		return ServiceDesired{}, err
	}
	snapshot, err := serviceconfig.Normalize(input.Snapshot)
	if err != nil {
		return ServiceDesired{}, err
	}
	updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
	err = store.Write(ctx, func(transaction *sql.Tx) error {
		if err := validateServiceVersion(ctx, transaction, input.ID, input.ProjectID, input.ExpectedUpdatedMillis); err != nil {
			return err
		}
		if err := validateServiceDependencies(ctx, transaction, input.ProjectID, input.ID, snapshot); err != nil {
			return err
		}
		if err := replaceServiceConfig(ctx, transaction, input.ID, input.ProjectID, snapshot, input.Enabled, input.ExpectedUpdatedMillis, updatedAt); err != nil {
			return err
		}
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.update", ServiceID: input.ID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: updatedAt,
		})
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.DesiredService(ctx, input.ID)
}

func (store *Store) RollbackService(ctx context.Context, input RollbackServiceInput) (ServiceDesired, error) {
	if input.DeploymentID == "" {
		return ServiceDesired{}, errors.New("rollback deployment ID is empty")
	}
	if err := validateServiceMutationIdentity(input.ID, input.ProjectID, input.ExpectedUpdatedMillis, input.AuditEventID, input.ActorID, input.ActorEmail, input.UpdatedAtMillis); err != nil {
		return ServiceDesired{}, err
	}
	updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var imageDigest string
		var snapshotJSON string
		var status string
		var enabled int
		var currentUpdated int64
		err := transaction.QueryRowContext(ctx, `
SELECT d.image_digest, d.snapshot_json, d.status, s.enabled, s.updated_at
FROM services s
JOIN deployments d ON d.service_id = s.id
WHERE s.id = ? AND s.project_id = ? AND d.id = ?`, input.ID, input.ProjectID, input.DeploymentID).Scan(
			&imageDigest, &snapshotJSON, &status, &enabled, &currentUpdated,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeploymentNotFound
		}
		if err != nil {
			return fmt.Errorf("load rollback deployment: %w", err)
		}
		if currentUpdated != input.ExpectedUpdatedMillis {
			return ErrServiceChanged
		}
		if status != "succeeded" {
			return ErrDeploymentNotSuccess
		}
		var snapshot serviceconfig.Snapshot
		if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
			return fmt.Errorf("decode rollback snapshot: %w", err)
		}
		pinned, err := serviceconfig.PinnedReference(snapshot.ImageReference, imageDigest)
		if err != nil {
			return err
		}
		snapshot.ImageReference = pinned
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
			ID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.rollback", ServiceID: input.ID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: updatedAt,
			Metadata: map[string]string{"deploymentId": input.DeploymentID},
		})
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.DesiredService(ctx, input.ID)
}

func (store *Store) RedeployService(ctx context.Context, input RedeployServiceInput) (ServiceDesired, error) {
	if err := validateServiceMutationIdentity(input.ID, input.ProjectID, input.ExpectedUpdatedMillis, input.AuditEventID, input.ActorID, input.ActorEmail, input.CreatedAtMillis); err != nil {
		return ServiceDesired{}, err
	}
	err := store.Write(ctx, func(transaction *sql.Tx) error {
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
			ID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.redeploy", ServiceID: input.ID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.CreatedAtMillis,
		})
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.DesiredService(ctx, input.ID)
}
