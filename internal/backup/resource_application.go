package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/backupcron"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

var ErrResourceRunner = errors.New("resource backup runner is not configured")

type ResourceApplicationStore interface {
	BackupPolicies(context.Context) ([]state.BackupPolicy, error)
	BackupPolicy(context.Context, string, string) (state.BackupPolicy, error)
	SetBackupPolicy(context.Context, state.SetBackupPolicy) (state.BackupPolicy, error)
	BackupHistory(context.Context, state.BackupHistoryQuery) ([]state.BackupRecord, error)
	Operation(context.Context, string) (state.Operation, error)
}

type ManualRunner interface {
	TryRunNow(context.Context, string, string, string, int) (state.BackupRecord, error)
}

type RestoreRunner interface {
	Start(context.Context, string, string, string, string, ResourceRestoreOptions, Actor) (state.Operation, error)
}

type ResourceApplication struct {
	store         ResourceApplicationStore
	worker        ManualRunner
	restores      RestoreRunner
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
	Restores      RestoreRunner
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
	TargetID       string
	Enabled        bool
	Cron           string
	RetentionCount int
	Actor          Actor
}

type PolicyResult struct {
	Policy          state.BackupPolicy
	NextRunAtMillis int64
	RequestID       string
}

type PolicyStatus struct {
	Policy          state.BackupPolicy
	NextRunAtMillis int64
}

func NewResourceApplication(config ResourceApplicationConfig) (*ResourceApplication, error) {
	if config.Store == nil || (config.Target == nil) != (config.TargetGate == nil) {
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
		store: config.Store, worker: config.Worker, restores: config.Restores,
		target: config.Target, targetGate: config.TargetGate,
		master: config.Master, remoteFactory: config.RemoteFactory, random: config.Random, now: config.Now,
	}, nil
}

func (application *ResourceApplication) Restore(
	ctx context.Context,
	kind, resourceID, targetID, generationID string,
	options ResourceRestoreOptions,
	actor Actor,
) (state.Operation, error) {
	if _, err := application.store.BackupPolicy(ctx, kind, resourceID); err != nil {
		return state.Operation{}, err
	}
	if application.restores == nil {
		return state.Operation{}, ErrResourceRestorer
	}
	return application.restores.Start(ctx, kind, resourceID, targetID, generationID, options, actor)
}

func (application *ResourceApplication) Operation(ctx context.Context, operationID string) (state.Operation, error) {
	return application.store.Operation(ctx, operationID)
}

func (application *ResourceApplication) Generations(
	ctx context.Context,
	kind, resourceID, targetID string,
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
	target, err := application.target.RuntimeTarget(ctx, targetID)
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

func (application *ResourceApplication) Policies(ctx context.Context) ([]PolicyStatus, error) {
	policies, err := application.store.BackupPolicies(ctx)
	if err != nil {
		return nil, err
	}
	return policyStatuses(policies, application.now())
}

func (application *ResourceApplication) Policy(ctx context.Context, kind, resourceID string) (PolicyStatus, error) {
	policy, err := application.store.BackupPolicy(ctx, kind, resourceID)
	if err != nil {
		return PolicyStatus{}, err
	}
	next, err := nextPolicyRun(policy, application.now())
	return PolicyStatus{Policy: policy, NextRunAtMillis: next}, err
}

func (application *ResourceApplication) SetPolicy(ctx context.Context, input PolicyInput) (PolicyResult, error) {
	if err := validateActor(input.Actor); err != nil {
		return PolicyResult{}, err
	}
	timestamp := application.now()
	nextRun, err := nextPolicyRun(state.BackupPolicy{
		Enabled: input.Enabled, Cron: input.Cron, TargetID: input.TargetID,
	}, timestamp)
	if err != nil {
		return PolicyResult{}, err
	}
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
		TargetID: input.TargetID, Enabled: input.Enabled, Cron: input.Cron, RetentionCount: input.RetentionCount,
		AuditEventID: auditID, ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: requestID,
		UpdatedAtMillis: timestamp.UnixMilli(),
	})
	return PolicyResult{Policy: policy, NextRunAtMillis: nextRun, RequestID: requestID}, err
}

func policyStatuses(policies []state.BackupPolicy, now time.Time) ([]PolicyStatus, error) {
	result := make([]PolicyStatus, len(policies))
	for index, policy := range policies {
		next, err := nextPolicyRun(policy, now)
		if err != nil {
			return nil, err
		}
		result[index] = PolicyStatus{Policy: policy, NextRunAtMillis: next}
	}
	return result, nil
}

func nextPolicyRun(policy state.BackupPolicy, now time.Time) (int64, error) {
	if !policy.Enabled {
		return 0, nil
	}
	if policy.TargetID == "" {
		return 0, errors.New("enabled backup policy has no storage target")
	}
	schedule, err := backupcron.Parse(policy.Cron)
	if err != nil {
		return 0, err
	}
	next, err := schedule.Next(now)
	if err != nil {
		return 0, err
	}
	return next.UnixMilli(), nil
}

func (application *ResourceApplication) RunNow(ctx context.Context, kind, resourceID, targetID string) (state.BackupRecord, error) {
	if application.worker == nil {
		return state.BackupRecord{}, ErrResourceRunner
	}
	policy, err := application.store.BackupPolicy(ctx, kind, resourceID)
	if err != nil {
		return state.BackupRecord{}, err
	}
	return application.worker.TryRunNow(ctx, kind, resourceID, targetID, policy.RetentionCount)
}

func (application *ResourceApplication) History(
	ctx context.Context,
	kind, resourceID, targetID string,
	beforeMillis int64,
	limit int,
) ([]state.BackupRecord, error) {
	if _, err := application.store.BackupPolicy(ctx, kind, resourceID); err != nil {
		return nil, err
	}
	return application.store.BackupHistory(ctx, state.BackupHistoryQuery{
		TargetID: targetID, ResourceKind: kind, ResourceID: resourceID, BeforeMillis: beforeMillis, Limit: limit,
	})
}
