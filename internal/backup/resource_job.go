package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrResourceTargetMissing = errors.New("resource backup target is not configured")
	ErrResourceExporter      = errors.New("resource backup exporter is not configured")
)

type ResourceExport struct {
	Reader          io.ReadCloser
	AttachmentPaths []string
	Release         func()
}

type ResourceExporter interface {
	Export(context.Context, string) (ResourceExport, error)
}

type ResourceExporterFunc func(context.Context, string) (ResourceExport, error)

func (exporter ResourceExporterFunc) Export(ctx context.Context, resourceID string) (ResourceExport, error) {
	return exporter(ctx, resourceID)
}

type ResourceJobConfig struct {
	Store         *state.Store
	Target        *TargetApplication
	TargetGate    *Gate
	Admission     *admission.Gate
	Growth        GrowthGate
	Master        cryptobox.MasterKey
	WorkRoot      string
	Exporters     map[string]ResourceExporter
	RemoteFactory ControlRemoteFactory
	Now           func() time.Time
	Random        io.Reader
}

type ResourceJob struct{ config ResourceJobConfig }

func NewResourceJob(config ResourceJobConfig) (*ResourceJob, error) {
	if config.Store == nil || config.Target == nil || config.TargetGate == nil || config.Admission == nil ||
		config.Growth == nil || !safeBackupRoot(config.WorkRoot) {
		return nil, errors.New("resource backup job configuration is incomplete")
	}
	for kind, exporter := range config.Exporters {
		if !validBackupResourceKind(kind) || exporter == nil {
			return nil, errors.New("resource backup exporter map is invalid")
		}
	}
	if config.RemoteFactory == nil {
		config.RemoteFactory = func(config remotes3.Config) (ControlRemote, error) { return remotes3.New(config) }
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &ResourceJob{config: config}, nil
}

func (job *ResourceJob) RunResource(
	ctx context.Context,
	resourceKind, resourceID, targetID string,
	scheduledOccurrenceMillis *int64,
	retentionCount int,
) (state.BackupRecord, error) {
	return job.runResource(ctx, resourceKind, resourceID, targetID, scheduledOccurrenceMillis, retentionCount, nil)
}

func (job *ResourceJob) RunResourceStarted(
	ctx context.Context,
	resourceKind, resourceID, targetID string,
	scheduledOccurrenceMillis *int64,
	retentionCount int,
	onStarted func(state.BackupRecord),
) (state.BackupRecord, error) {
	return job.runResource(ctx, resourceKind, resourceID, targetID, scheduledOccurrenceMillis, retentionCount, onStarted)
}

func (job *ResourceJob) runResource(
	ctx context.Context,
	resourceKind, resourceID, targetID string,
	scheduledOccurrenceMillis *int64,
	retentionCount int,
	onStarted func(state.BackupRecord),
) (state.BackupRecord, error) {
	if !validBackupResourceKind(resourceKind) || !validControlIdentifier(resourceID) || targetID == "" || retentionCount < 1 || retentionCount > 100 ||
		(scheduledOccurrenceMillis != nil && *scheduledOccurrenceMillis <= 0) {
		return state.BackupRecord{}, errors.New("resource backup request is invalid")
	}
	exporter := job.config.Exporters[resourceKind]
	if exporter == nil {
		return state.BackupRecord{}, ErrResourceExporter
	}
	releaseTarget, acquired := job.config.TargetGate.TryAcquire()
	if !acquired {
		return state.BackupRecord{}, ErrTargetBusy
	}
	defer releaseTarget()
	target, err := job.config.Target.RuntimeTarget(ctx, targetID)
	if errors.Is(err, state.ErrBackupTargetNotFound) {
		return state.BackupRecord{}, ErrResourceTargetMissing
	}
	if err != nil {
		return state.BackupRecord{}, err
	}
	if err := job.config.Growth.PermitGrowth(ctx); err != nil {
		return state.BackupRecord{}, err
	}
	startedAt := job.config.Now()
	backupID, err := id.NewWith(startedAt, job.config.Random)
	if err != nil {
		return state.BackupRecord{}, err
	}
	generationID, err := id.NewWith(startedAt, job.config.Random)
	if err != nil {
		return state.BackupRecord{}, err
	}
	lease, err := job.config.Admission.Begin("resource_backup", backupID)
	if err != nil {
		return state.BackupRecord{}, err
	}
	defer lease.Release()
	if err := job.config.Store.BeginBackup(ctx, state.BeginBackup{
		ID: backupID, TargetID: targetID, ResourceKind: resourceKind, ResourceID: resourceID,
		ScheduledOccurrenceMillis: scheduledOccurrenceMillis, GenerationID: generationID,
		StartedAtMillis: startedAt.UnixMilli(),
	}); err != nil {
		return state.BackupRecord{}, err
	}
	if onStarted != nil {
		onStarted(state.BackupRecord{
			ID: backupID, TargetID: targetID, ResourceKind: resourceKind, ResourceID: resourceID,
			ScheduledOccurrenceMillis: scheduledOccurrenceMillis, GenerationID: generationID,
			Status: "running", StartedAtMillis: startedAt.UnixMilli(),
		})
	}
	exported, err := exporter.Export(ctx, resourceID)
	if err != nil {
		return job.fail(ctx, backupID, resourceKind, err)
	}
	if exported.Reader == nil {
		if exported.Release != nil {
			exported.Release()
		}
		return job.fail(ctx, backupID, resourceKind, errors.New("resource exporter returned nil reader"))
	}
	if exported.Release != nil {
		defer exported.Release()
	}
	built, buildErr := BuildResource(ctx, ResourceBuildConfig{
		Master: job.config.Master, ResourceKind: resourceKind, ResourceID: resourceID,
		GenerationID: generationID, WorkRoot: job.config.WorkRoot, CreatedAt: startedAt,
		Random: job.config.Random, AttachmentPaths: exported.AttachmentPaths,
	}, exported.Reader)
	closeErr := exported.Reader.Close()
	if buildErr != nil || closeErr != nil {
		return job.fail(ctx, backupID, resourceKind, errors.Join(buildErr, closeErr))
	}
	defer os.RemoveAll(built.WorkDirectory)
	remote, err := job.config.RemoteFactory(remotes3.Config{
		Endpoint: target.Endpoint, Region: target.Region, Bucket: target.Bucket, Prefix: target.Prefix,
		AccessKeyID: target.AccessKeyID, SecretAccessKey: target.SecretAccessKey,
	})
	if err != nil {
		return job.fail(ctx, backupID, resourceKind, err)
	}
	if err := PublishResource(ctx, remote, job.config.Master, built); err != nil {
		return job.fail(ctx, backupID, resourceKind, err)
	}
	cleanupErr := ApplyResourceRetention(ctx, remote, resourceKind, resourceID, retentionCount)
	if err := job.config.Store.FinishBackup(ctx, state.FinishBackup{
		ID: backupID, Status: "succeeded", SizeBytes: built.Completion.RemoteSize,
		FinishedAtMillis: job.config.Now().UnixMilli(),
	}); err != nil {
		return state.BackupRecord{}, errors.Join(cleanupErr, err)
	}
	record, recordErr := job.config.Store.Backup(ctx, backupID)
	if cleanupErr != nil {
		return record, &PublishedResourceError{Err: cleanupErr}
	}
	return record, recordErr
}

func (job *ResourceJob) fail(ctx context.Context, backupID, resourceKind string, cause error) (state.BackupRecord, error) {
	finishErr := job.config.Store.FinishBackup(ctx, state.FinishBackup{
		ID: backupID, Status: "failed", ErrorCode: resourceKind + "_backup_failed",
		ErrorMessage: boundedBackupError(cause), FinishedAtMillis: job.config.Now().UnixMilli(),
	})
	record, recordErr := job.config.Store.Backup(ctx, backupID)
	return record, errors.Join(cause, finishErr, recordErr)
}

type PublishedResourceError struct{ Err error }

func (failure *PublishedResourceError) Error() string {
	return "resource generation was published but retention cleanup failed: " + failure.Err.Error()
}

func (failure *PublishedResourceError) Unwrap() error { return failure.Err }
