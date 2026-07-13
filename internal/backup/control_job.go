package backup

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

var ErrControlTargetMissing = errors.New("control backup target is not configured")

type GrowthGate interface {
	PermitGrowth(context.Context) error
}

type ControlRemoteFactory func(remotes3.Config) (ControlRemote, error)

type ControlJobConfig struct {
	Store          *state.Store
	Target         *TargetApplication
	TargetGate     *Gate
	Admission      *admission.Gate
	Growth         GrowthGate
	Master         cryptobox.MasterKey
	InstallationID string
	WorkRoot       string
	ExpectedUID    int
	PublicKey      ed25519.PublicKey
	ReleaseSlot    func() (string, error)
	RemoteFactory  ControlRemoteFactory
	Now            func() time.Time
	Random         io.Reader
}

type ControlJob struct{ config ControlJobConfig }

func NewControlJob(config ControlJobConfig) (*ControlJob, error) {
	if config.Store == nil || config.Target == nil || config.TargetGate == nil || config.Admission == nil || config.Growth == nil ||
		config.InstallationID == "" || !safeBackupRoot(config.WorkRoot) || config.ExpectedUID < 0 ||
		len(config.PublicKey) != ed25519.PublicKeySize || config.ReleaseSlot == nil {
		return nil, errors.New("control backup job configuration is incomplete")
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
	return &ControlJob{config: config}, nil
}

func (job *ControlJob) RunControl(ctx context.Context) error {
	releaseTarget, acquired := job.config.TargetGate.TryAcquire()
	if !acquired {
		return ErrTargetBusy
	}
	defer releaseTarget()
	target, err := job.config.Target.RuntimeTarget(ctx)
	if errors.Is(err, state.ErrBackupTargetNotFound) {
		return ErrControlTargetMissing
	}
	if err != nil {
		return err
	}
	if err := job.config.Growth.PermitGrowth(ctx); err != nil {
		return err
	}
	startedAt := job.config.Now()
	backupID, err := id.NewWith(startedAt, job.config.Random)
	if err != nil {
		return err
	}
	generationID, err := id.NewWith(startedAt, job.config.Random)
	if err != nil {
		return err
	}
	lease, err := job.config.Admission.Begin("control_backup", backupID)
	if err != nil {
		return err
	}
	defer lease.Release()
	if err := job.config.Store.BeginBackup(ctx, state.BeginBackup{
		ID: backupID, ResourceKind: "control", ResourceID: job.config.InstallationID,
		GenerationID: generationID, StartedAtMillis: startedAt.UnixMilli(),
	}); err != nil {
		return err
	}
	releaseSlot, err := job.config.ReleaseSlot()
	if err != nil {
		return job.fail(ctx, backupID, err)
	}
	built, err := BuildControl(ctx, ControlBuildConfig{
		Store: job.config.Store, Master: job.config.Master, InstallationID: job.config.InstallationID,
		GenerationID: generationID, ReleaseSlot: releaseSlot, WorkRoot: job.config.WorkRoot,
		ExpectedUID: job.config.ExpectedUID, PublicKey: job.config.PublicKey,
		CreatedAt: startedAt, Random: job.config.Random,
	})
	if err != nil {
		return job.fail(ctx, backupID, err)
	}
	defer os.RemoveAll(built.WorkDirectory)
	remote, err := job.config.RemoteFactory(remotes3.Config{
		Endpoint: target.Endpoint, Region: target.Region, Bucket: target.Bucket, Prefix: target.Prefix,
		AccessKeyID: target.AccessKeyID, SecretAccessKey: target.SecretAccessKey,
	})
	if err != nil {
		return job.fail(ctx, backupID, err)
	}
	publishErr := PublishControl(ctx, remote, job.config.Master, built)
	var published *PublishedControlError
	if publishErr != nil && !errors.As(publishErr, &published) {
		return job.fail(ctx, backupID, publishErr)
	}
	size, err := controlPublishedSize(built)
	if err != nil {
		return job.fail(ctx, backupID, err)
	}
	if err := job.config.Store.FinishBackup(ctx, state.FinishBackup{
		ID: backupID, Status: "succeeded", SizeBytes: size, FinishedAtMillis: job.config.Now().UnixMilli(),
	}); err != nil {
		return errors.Join(publishErr, err)
	}
	return publishErr
}

func (job *ControlJob) fail(ctx context.Context, backupID string, cause error) error {
	message := boundedBackupError(cause)
	finishErr := job.config.Store.FinishBackup(ctx, state.FinishBackup{
		ID: backupID, Status: "failed", ErrorCode: "control_backup_failed",
		ErrorMessage: message, FinishedAtMillis: job.config.Now().UnixMilli(),
	})
	return errors.Join(cause, finishErr)
}

func controlPublishedSize(build ControlBuild) (int64, error) {
	size := int64(len(build.EnvelopeBytes) + len(build.CompletionBytes))
	for _, chunk := range build.Chunks {
		if chunk.CiphertextSize < 0 || size > math.MaxInt64-int64(chunk.CiphertextSize) {
			return 0, errors.New("control backup size overflow")
		}
		size += int64(chunk.CiphertextSize)
	}
	return size, nil
}

func boundedBackupError(err error) string {
	value := strings.ToValidUTF8(err.Error(), "�")
	if len(value) <= 4096 {
		return value
	}
	value = value[:4096]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

var _ ControlRunner = (*ControlJob)(nil)
