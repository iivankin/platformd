package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/remotes3"
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
	store         ResourceApplicationStore
	worker        ManualRunner
	target        *TargetApplication
	targetGate    *Gate
	master        cryptobox.MasterKey
	remoteFactory ControlRemoteFactory
	random        io.Reader
	now           func() time.Time
}

type ResourceApplicationConfig struct {
	Store         ResourceApplicationStore
	Worker        ManualRunner
	Target        *TargetApplication
	TargetGate    *Gate
	Master        cryptobox.MasterKey
	RemoteFactory ControlRemoteFactory
	Random        io.Reader
	Now           func() time.Time
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

func NewResourceApplication(config ResourceApplicationConfig) (*ResourceApplication, error) {
	if config.Store == nil || config.Worker == nil || (config.Target == nil) != (config.TargetGate == nil) {
		return nil, errors.New("resource backup application dependencies are incomplete")
	}
	if config.RemoteFactory == nil {
		config.RemoteFactory = func(config remotes3.Config) (ControlRemote, error) { return remotes3.New(config) }
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &ResourceApplication{
		store: config.Store, worker: config.Worker, target: config.Target, targetGate: config.TargetGate,
		master: config.Master, remoteFactory: config.RemoteFactory, random: config.Random, now: config.Now,
	}, nil
}

func (application *ResourceApplication) Generations(
	ctx context.Context,
	kind, resourceID string,
) ([]ResourceCompletion, error) {
	if _, err := application.store.BackupPolicy(ctx, kind, resourceID); err != nil {
		return nil, err
	}
	if application.target == nil || application.targetGate == nil {
		return nil, errors.New("resource backup remote access is not configured")
	}
	release, acquired := application.targetGate.TryAcquire()
	if !acquired {
		return nil, ErrTargetBusy
	}
	defer release()
	target, err := application.target.RuntimeTarget(ctx)
	if errors.Is(err, state.ErrBackupTargetNotFound) {
		return nil, ErrResourceTargetMissing
	}
	if err != nil {
		return nil, err
	}
	remote, err := application.remoteFactory(remotes3.Config{
		Endpoint: target.Endpoint, Region: target.Region, Bucket: target.Bucket, Prefix: target.Prefix,
		AccessKeyID: target.AccessKeyID, SecretAccessKey: target.SecretAccessKey,
	})
	if err != nil {
		return nil, err
	}
	return ListResourceGenerations(ctx, remote, kind, resourceID)
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
