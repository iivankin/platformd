package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

const targetSecretDomain = "platformd/sqlite/backup-target/v1"

var (
	ErrTargetBusy     = errors.New("backup target is busy")
	ErrEmbeddedTarget = errors.New("embedded object storage cannot be used as the remote backup target")
	ErrInvalidInput   = errors.New("invalid backup input")
)

type TargetStore interface {
	Installation(context.Context) (state.Installation, error)
	BackupTargets(context.Context) ([]state.BackupTarget, error)
	BackupTarget(context.Context, string) (state.BackupTarget, error)
	ControlBackupTargetID(context.Context) (string, error)
	SetBackupTarget(context.Context, state.SetBackupTarget) (state.BackupTarget, error)
	SetControlBackupTarget(context.Context, state.SetControlBackupTarget) error
	DeleteBackupTarget(context.Context, state.DeleteBackupTarget) error
	EmbeddedObjectStoreHostnameExists(context.Context, string) (bool, error)
}

type Probe interface{ Probe(context.Context) error }
type RemoteFactory func(remotes3.Config) (Probe, error)

type Actor struct{ Kind, ID, Email string }

type TargetInput struct {
	ID, Name, Endpoint, Region, Bucket, Prefix, AccessKeyID, SecretAccessKey string
	Actor                                                                    Actor
}

type Target struct {
	ID, Name, Endpoint, Region, Bucket, Prefix, AccessKeyID string
	CreatedAtMillis, UpdatedAtMillis                        int64
}

type RuntimeTarget struct {
	Target
	SecretAccessKey string
}

type TargetResult struct {
	Target    Target
	RequestID string
}

type ControlTargetResult struct {
	TargetID  string
	RequestID string
}

type Gate struct {
	mutex sync.Mutex
	busy  bool
}

func NewGate() *Gate { return &Gate{} }

func (gate *Gate) TryAcquire() (func(), bool) {
	gate.mutex.Lock()
	if gate.busy {
		gate.mutex.Unlock()
		return nil, false
	}
	gate.busy = true
	gate.mutex.Unlock()
	return sync.OnceFunc(func() {
		gate.mutex.Lock()
		gate.busy = false
		gate.mutex.Unlock()
	}), true
}

type TargetApplication struct {
	store   TargetStore
	master  cryptobox.MasterKey
	gate    *Gate
	factory RemoteFactory
	random  io.Reader
	now     func() time.Time
}

func NewTargetApplication(store TargetStore, master cryptobox.MasterKey, gate *Gate, factory RemoteFactory, random io.Reader, now func() time.Time) (*TargetApplication, error) {
	if store == nil {
		return nil, errors.New("backup target store is required")
	}
	if gate == nil {
		gate = NewGate()
	}
	if factory == nil {
		factory = func(config remotes3.Config) (Probe, error) { return remotes3.New(config) }
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &TargetApplication{store: store, master: master, gate: gate, factory: factory, random: random, now: now}, nil
}

func (application *TargetApplication) Targets(ctx context.Context) ([]Target, error) {
	stored, err := application.store.BackupTargets(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Target, len(stored))
	for index := range stored {
		result[index] = publicTarget(stored[index])
	}
	return result, nil
}

func (application *TargetApplication) ControlTargetID(ctx context.Context) (string, error) {
	return application.store.ControlBackupTargetID(ctx)
}

func (application *TargetApplication) RuntimeTarget(ctx context.Context, targetID string) (RuntimeTarget, error) {
	stored, err := application.store.BackupTarget(ctx, targetID)
	if err != nil {
		return RuntimeTarget{}, err
	}
	installation, err := application.store.Installation(ctx)
	if err != nil {
		return RuntimeTarget{}, err
	}
	secret, err := openTargetSecret(application.master, installation.ID, stored.SecretAccessKeyEncrypted)
	if err != nil {
		return RuntimeTarget{}, err
	}
	return RuntimeTarget{Target: publicTarget(stored), SecretAccessKey: secret}, nil
}

func (application *TargetApplication) ControlRuntimeTarget(ctx context.Context) (RuntimeTarget, error) {
	targetID, err := application.store.ControlBackupTargetID(ctx)
	if err != nil {
		return RuntimeTarget{}, err
	}
	if targetID == "" {
		return RuntimeTarget{}, state.ErrBackupTargetNotFound
	}
	return application.RuntimeTarget(ctx, targetID)
}

func (application *TargetApplication) SetTarget(ctx context.Context, input TargetInput) (TargetResult, error) {
	if err := validateActor(input.Actor); err != nil || strings.TrimSpace(input.Name) == "" {
		return TargetResult{}, ErrInvalidInput
	}
	release, acquired := application.gate.TryAcquire()
	if !acquired {
		return TargetResult{}, ErrTargetBusy
	}
	defer release()
	if input.ID != "" {
		if _, err := application.store.BackupTarget(ctx, input.ID); err != nil {
			return TargetResult{}, err
		}
	}
	canonical, err := remotes3.CanonicalConfig(remotes3.Config{
		Endpoint: input.Endpoint, Region: input.Region, Bucket: input.Bucket, Prefix: input.Prefix,
		AccessKeyID: input.AccessKeyID, SecretAccessKey: input.SecretAccessKey, Random: application.random,
	})
	if err != nil {
		return TargetResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	endpoint, err := url.Parse(canonical.Endpoint)
	if err != nil {
		return TargetResult{}, fmt.Errorf("%w: endpoint is invalid", ErrInvalidInput)
	}
	embedded, err := application.store.EmbeddedObjectStoreHostnameExists(ctx, strings.ToLower(endpoint.Hostname()))
	if err != nil {
		return TargetResult{}, err
	}
	if embedded {
		return TargetResult{}, ErrEmbeddedTarget
	}
	remote, err := application.factory(canonical)
	if err != nil {
		return TargetResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := remote.Probe(ctx); err != nil {
		return TargetResult{}, err
	}
	installation, err := application.store.Installation(ctx)
	if err != nil {
		return TargetResult{}, err
	}
	encrypted, err := SealTargetSecret(application.master, installation.ID, canonical.SecretAccessKey)
	if err != nil {
		return TargetResult{}, err
	}
	timestamp := application.now()
	targetID := input.ID
	if targetID == "" {
		targetID, err = id.NewWith(timestamp, application.random)
		if err != nil {
			return TargetResult{}, err
		}
	}
	auditID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return TargetResult{}, err
	}
	requestID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return TargetResult{}, err
	}
	stored, err := application.store.SetBackupTarget(ctx, state.SetBackupTarget{
		Target: state.BackupTarget{
			ID: targetID, Name: strings.TrimSpace(input.Name), Endpoint: canonical.Endpoint,
			Region: canonical.Region, Bucket: canonical.Bucket, Prefix: canonical.Prefix,
			AccessKeyID: canonical.AccessKeyID, SecretAccessKeyEncrypted: encrypted,
		},
		AuditEventID: auditID, ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: requestID, UpdatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return TargetResult{}, err
	}
	return TargetResult{Target: publicTarget(stored), RequestID: requestID}, nil
}

func (application *TargetApplication) SetControlTarget(ctx context.Context, targetID string, actor Actor) (ControlTargetResult, error) {
	if err := validateActor(actor); err != nil {
		return ControlTargetResult{}, err
	}
	release, acquired := application.gate.TryAcquire()
	if !acquired {
		return ControlTargetResult{}, ErrTargetBusy
	}
	defer release()
	timestamp := application.now()
	auditID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return ControlTargetResult{}, err
	}
	requestID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return ControlTargetResult{}, err
	}
	err = application.store.SetControlBackupTarget(ctx, state.SetControlBackupTarget{
		TargetID: targetID, AuditEventID: auditID, ActorKind: actor.Kind, ActorID: actor.ID,
		ActorEmail: actor.Email, RequestCorrelationID: requestID, UpdatedAtMillis: timestamp.UnixMilli(),
	})
	return ControlTargetResult{TargetID: targetID, RequestID: requestID}, err
}

func (application *TargetApplication) DeleteTarget(ctx context.Context, targetID string, actor Actor) (string, error) {
	if targetID == "" || validateActor(actor) != nil {
		return "", ErrInvalidInput
	}
	release, acquired := application.gate.TryAcquire()
	if !acquired {
		return "", ErrTargetBusy
	}
	defer release()
	timestamp := application.now()
	auditID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return "", err
	}
	requestID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return "", err
	}
	err = application.store.DeleteBackupTarget(ctx, state.DeleteBackupTarget{
		TargetID: targetID, AuditEventID: auditID, ActorKind: actor.Kind, ActorID: actor.ID,
		ActorEmail: actor.Email, RequestCorrelationID: requestID, DeletedAtMillis: timestamp.UnixMilli(),
	})
	return requestID, err
}

func publicTarget(target state.BackupTarget) Target {
	return Target{ID: target.ID, Name: target.Name, Endpoint: target.Endpoint, Region: target.Region,
		Bucket: target.Bucket, Prefix: target.Prefix, AccessKeyID: target.AccessKeyID,
		CreatedAtMillis: target.CreatedAtMillis, UpdatedAtMillis: target.UpdatedAtMillis}
}

func validateActor(actor Actor) error {
	if actor.ID == "" || (actor.Kind != "access" && actor.Kind != "token" && actor.Kind != "local_root") ||
		(actor.Kind == "access" && actor.Email == "") {
		return fmt.Errorf("%w: backup actor is incomplete", ErrInvalidInput)
	}
	return nil
}

func SealTargetSecret(master cryptobox.MasterKey, installationID, secret string) ([]byte, error) {
	if installationID == "" || secret == "" {
		return nil, errors.New("backup target secret input is incomplete")
	}
	box, err := cryptobox.NewBox(master, []byte(installationID), targetSecretDomain)
	if err != nil {
		return nil, err
	}
	plaintext := []byte(secret)
	defer clear(plaintext)
	// Keep the installation-scoped AAD so the v9 singleton target can be
	// migrated in SQL without ever decrypting its secret.
	return box.Seal(plaintext, []byte(installationID+":backup-target-secret"))
}

func openTargetSecret(master cryptobox.MasterKey, installationID string, encrypted []byte) (string, error) {
	if installationID == "" || len(encrypted) == 0 {
		return "", errors.New("backup target secret input is incomplete")
	}
	box, err := cryptobox.NewBox(master, []byte(installationID), targetSecretDomain)
	if err != nil {
		return "", err
	}
	plaintext, err := box.Open(encrypted, []byte(installationID+":backup-target-secret"))
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	return string(plaintext), nil
}
