package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

const recoveryRetryDelay = 30 * time.Second

type recoveryStore interface {
	ControlResources(context.Context) (state.ControlResourceIDs, error)
	CompleteRecovery(context.Context, state.CompleteRecovery) error
}

type recoveryPolicyStore interface {
	BackupPolicy(context.Context, string, string) (state.BackupPolicy, error)
}

type recoveryConfig struct {
	Store        recoveryStore
	Target       *backup.TargetApplication
	TargetGate   *backup.Gate
	Admission    *admission.Gate
	Master       cryptobox.MasterKey
	Installation state.Installation
	Runtime      *runtimeStack
	Registry     *registry.Application
	ObjectStore  *objectstore.Application
	Progress     *recoveryProgress
	Remote       func(remotes3.Config) (backup.ControlRemote, error)
	Now          func() time.Time
	NewID        func() (string, error)
}

type recoveryAttempt struct {
	execute func(context.Context) error
}

type recoveryPlan struct {
	resources func(context.Context) (state.ControlResourceIDs, error)
	restore   func(context.Context, string, string) error
	complete  func(context.Context) error
}

func (attempt recoveryAttempt) run(ctx context.Context) error {
	if attempt.execute == nil {
		return errors.New("recovery attempt is not configured")
	}
	return attempt.execute(ctx)
}

func (plan recoveryPlan) run(ctx context.Context) error {
	resources, err := plan.resources(ctx)
	if err != nil {
		return err
	}
	ordered := []struct {
		kind string
		ids  []string
	}{
		{kind: "volume", ids: resources.Volumes},
		{kind: "registry", ids: resources.RegistryRepositories},
		{kind: "object_store", ids: resources.ObjectStores},
		{kind: "postgres", ids: resources.Postgres},
		{kind: "redis", ids: resources.Redis},
	}
	for _, group := range ordered {
		for _, resourceID := range group.ids {
			if err := plan.restore(ctx, group.kind, resourceID); err != nil {
				return fmt.Errorf("restore %s %s: %w", group.kind, resourceID, err)
			}
		}
	}
	return plan.complete(ctx)
}

func newRecoveryAttempt(config recoveryConfig) (recoveryAttempt, error) {
	if config.Store == nil || config.Target == nil || config.TargetGate == nil || config.Admission == nil ||
		config.Installation.ID == "" || config.Runtime == nil || config.Registry == nil ||
		config.ObjectStore == nil || config.Progress == nil {
		return recoveryAttempt{}, errors.New("recovery dependencies are incomplete")
	}
	if config.Remote == nil {
		config.Remote = func(value remotes3.Config) (backup.ControlRemote, error) {
			return remotes3.New(value)
		}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewID == nil {
		config.NewID = id.New
	}
	return recoveryAttempt{execute: func(ctx context.Context) error {
		lease, err := config.Admission.Begin("disaster_restore", config.Installation.ID)
		if err != nil {
			return err
		}
		defer lease.Release()
		release, acquired := config.TargetGate.TryAcquire()
		if !acquired {
			return backup.ErrTargetBusy
		}
		defer release()
		target, err := config.Target.ControlRuntimeTarget(ctx)
		if err != nil {
			return err
		}
		_, err = config.Remote(remotes3.Config{
			Endpoint: target.Endpoint, Region: target.Region, Bucket: target.Bucket,
			Prefix: target.Prefix, AccessKeyID: target.AccessKeyID,
			SecretAccessKey: target.SecretAccessKey,
		})
		if err != nil {
			return err
		}
		plan := recoveryPlan{
			resources: config.Store.ControlResources,
			restore: func(ctx context.Context, kind, resourceID string) error {
				if config.Progress.satisfied(kind, resourceID) {
					return nil
				}
				restorer, err := recoveryRestorer(config, kind)
				if err != nil {
					return err
				}
				policyStore, ok := config.Store.(recoveryPolicyStore)
				if !ok {
					return errors.New("recovery backup policy store is unavailable")
				}
				policy, err := policyStore.BackupPolicy(ctx, kind, resourceID)
				if err != nil {
					return err
				}
				if policy.TargetID == "" {
					config.Progress.markLatest(kind, resourceID, backup.ResourceCompletion{}, false)
					return nil
				}
				resourceTarget, err := config.Target.RuntimeTarget(ctx, policy.TargetID)
				if err != nil {
					return err
				}
				remote, err := config.Remote(remotes3.Config{
					Endpoint: resourceTarget.Endpoint, Region: resourceTarget.Region,
					Bucket: resourceTarget.Bucket, Prefix: resourceTarget.Prefix,
					AccessKeyID: resourceTarget.AccessKeyID, SecretAccessKey: resourceTarget.SecretAccessKey,
				})
				if err != nil {
					return err
				}
				completion, found, err := backup.RestoreLatestResource(ctx, backup.LatestResourceRestore{
					Remote: remote, Master: config.Master, ResourceKind: kind, ResourceID: resourceID,
					Restorer: restorer, Options: backup.ResourceRestoreOptions{
						Mode: "replace", DestructiveConfirmed: true,
					},
					Actor: backup.Actor{Kind: "system", ID: "disaster_restore"},
				})
				if err != nil {
					return err
				}
				config.Progress.markLatest(kind, resourceID, completion, found)
				return nil
			},
			complete: func(ctx context.Context) error {
				auditID, err := config.NewID()
				if err != nil {
					return err
				}
				return config.Store.CompleteRecovery(ctx, state.CompleteRecovery{
					InstallationID: config.Installation.ID, AuditEventID: auditID,
					CompletedAtMillis: config.Now().UnixMilli(),
				})
			},
		}
		return plan.run(ctx)
	}}, nil
}

func recoveryRestorer(config recoveryConfig, kind string) (backup.ResourceRestorer, error) {
	var volumeConfig []ordinaryVolumeBackupConfig
	if store, ok := config.Store.(ordinaryVolumeRepository); ok {
		volumeConfig = append(volumeConfig, ordinaryVolumeBackupConfig{Store: store, Root: config.Runtime.paths.VolumesRoot})
	}
	restorer := resourceRestorers(config.Runtime, config.Registry, config.ObjectStore, volumeConfig...)[kind]
	if restorer == nil {
		return nil, errors.New("recovery resource kind is unsupported")
	}
	return restorer, nil
}

func startRecoveryLoop(
	ctx context.Context,
	attempt recoveryAttempt,
	progress *recoveryProgress,
	onComplete func(),
) {
	go func() {
		for {
			progress.beginAttempt()
			if err := attempt.run(ctx); err == nil {
				onComplete()
				return
			} else if ctx.Err() == nil {
				progress.recordFailure(err)
				log.Printf("automatic disaster recovery failed; retrying in %s: %v", recoveryRetryDelay, err)
			}
			timer := time.NewTimer(recoveryRetryDelay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			case <-progress.retry:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
		}
	}()
}
