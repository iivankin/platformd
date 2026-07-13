package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

type ResourceApplicationStore interface {
	BackupPolicies(context.Context) ([]state.BackupPolicy, error)
	BackupPolicy(context.Context, string, string) (state.BackupPolicy, error)
	SetBackupPolicy(context.Context, state.SetBackupPolicy) (state.BackupPolicy, error)
	BackupHistory(context.Context, state.BackupHistoryQuery) ([]state.BackupRecord, error)
}

type ManualRunner interface {
	TryRunNow(context.Context, string, string, int) (state.BackupRecord, error)
}

type ResourceApplication struct {
	store  ResourceApplicationStore
	worker ManualRunner
	random io.Reader
	now    func() time.Time
}

type PolicyInput struct {
	ResourceKind   string
	ResourceID     string
	Enabled        bool
	Cron           string
	RetentionCount int
	Actor          Actor
}

type PolicyResult struct {
	Policy    state.BackupPolicy
	RequestID string
}

func NewResourceApplication(
	store ResourceApplicationStore,
	worker ManualRunner,
	random io.Reader,
	now func() time.Time,
) (*ResourceApplication, error) {
	if store == nil || worker == nil {
		return nil, errors.New("resource backup application dependencies are incomplete")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &ResourceApplication{store: store, worker: worker, random: random, now: now}, nil
}

func (application *ResourceApplication) Policies(ctx context.Context) ([]state.BackupPolicy, error) {
	return application.store.BackupPolicies(ctx)
}

func (application *ResourceApplication) Policy(ctx context.Context, kind, resourceID string) (state.BackupPolicy, error) {
	return application.store.BackupPolicy(ctx, kind, resourceID)
}

func (application *ResourceApplication) SetPolicy(ctx context.Context, input PolicyInput) (PolicyResult, error) {
	if err := validateActor(input.Actor); err != nil {
		return PolicyResult{}, err
	}
	timestamp := application.now()
	auditID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return PolicyResult{}, err
	}
	requestID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return PolicyResult{}, err
	}
	policy, err := application.store.SetBackupPolicy(ctx, state.SetBackupPolicy{
		ResourceKind: input.ResourceKind, ResourceID: input.ResourceID,
		Enabled: input.Enabled, Cron: input.Cron, RetentionCount: input.RetentionCount,
		AuditEventID: auditID, ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: requestID,
		UpdatedAtMillis: timestamp.UnixMilli(),
	})
	return PolicyResult{Policy: policy, RequestID: requestID}, err
}

func (application *ResourceApplication) RunNow(ctx context.Context, kind, resourceID string) (state.BackupRecord, error) {
	policy, err := application.store.BackupPolicy(ctx, kind, resourceID)
	if err != nil {
		return state.BackupRecord{}, err
	}
	return application.worker.TryRunNow(ctx, kind, resourceID, policy.RetentionCount)
}

func (application *ResourceApplication) History(
	ctx context.Context,
	kind, resourceID string,
	beforeMillis int64,
	limit int,
) ([]state.BackupRecord, error) {
	if _, err := application.store.BackupPolicy(ctx, kind, resourceID); err != nil {
		return nil, err
	}
	return application.store.BackupHistory(ctx, state.BackupHistoryQuery{
		ResourceKind: kind, ResourceID: resourceID, BeforeMillis: beforeMillis, Limit: limit,
	})
}
