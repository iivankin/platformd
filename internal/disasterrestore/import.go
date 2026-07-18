package disasterrestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

type ImportPayload struct {
	DatabasePath           string       `json:"databasePath"`
	ExpectedInstallationID string       `json:"expectedInstallationId"`
	ExpectedSchemaVersion  int          `json:"expectedSchemaVersion"`
	MasterRecoveryKey      string       `json:"masterRecoveryKey"`
	Remote                 ImportTarget `json:"remote"`
	AccessTeamDomain       *string      `json:"accessTeamDomain,omitempty"`
	AccessAudience         *string      `json:"accessAudience,omitempty"`
	AuditEventID           string       `json:"auditEventId"`
	ImportedAtMillis       int64        `json:"importedAt"`
	ExpectedUID            int          `json:"expectedUid"`
}

type ImportTarget struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

func (target ImportTarget) config() remotes3.Config {
	return remotes3.Config{
		Endpoint: target.Endpoint, Region: target.Region, Bucket: target.Bucket, Prefix: target.Prefix,
		AccessKeyID: target.AccessKeyID, SecretAccessKey: target.SecretAccessKey,
	}
}

func importTarget(config remotes3.Config) ImportTarget {
	return ImportTarget{
		Endpoint: config.Endpoint, Region: config.Region, Bucket: config.Bucket, Prefix: config.Prefix,
		AccessKeyID: config.AccessKeyID, SecretAccessKey: config.SecretAccessKey,
	}
}

type ImportResult struct {
	AdminHostname        string `json:"adminHostname"`
	OriginCertificatePEM string `json:"originCertificatePem"`
}

func ReadImportPayload(reader io.Reader) (ImportPayload, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, maximumInputBytes+1))
	decoder.DisallowUnknownFields()
	var payload ImportPayload
	if err := decoder.Decode(&payload); err != nil {
		return ImportPayload{}, err
	}
	if decoder.InputOffset() > maximumInputBytes || decoder.Decode(&struct{}{}) != io.EOF {
		return ImportPayload{}, errors.New("restore import payload is oversized or contains trailing JSON")
	}
	return payload, nil
}

func ImportSnapshot(ctx context.Context, payload ImportPayload) (ImportResult, error) {
	if !filepath.IsAbs(payload.DatabasePath) || filepath.Clean(payload.DatabasePath) != payload.DatabasePath ||
		payload.DatabasePath == string(filepath.Separator) || payload.ExpectedInstallationID == "" ||
		payload.ExpectedSchemaVersion < 1 || payload.AuditEventID == "" || payload.ImportedAtMillis <= 0 ||
		payload.ExpectedUID < 0 || (payload.AccessTeamDomain == nil) != (payload.AccessAudience == nil) {
		return ImportResult{}, errors.New("restore import payload is incomplete")
	}
	if os.Geteuid() != payload.ExpectedUID {
		return ImportResult{}, errors.New("restore importer must run as the installation owner")
	}
	if state.SupportedSchemaVersion() != payload.ExpectedSchemaVersion {
		return ImportResult{}, fmt.Errorf(
			"saved platformd schema version = %d, control snapshot schema version = %d",
			state.SupportedSchemaVersion(), payload.ExpectedSchemaVersion,
		)
	}
	master, err := masterkey.ParseRecoveryString(payload.MasterRecoveryKey)
	if err != nil {
		return ImportResult{}, err
	}
	canonical, err := remotes3.CanonicalConfig(payload.Remote.config())
	if err != nil {
		return ImportResult{}, err
	}
	if payload.AccessTeamDomain != nil {
		team, audience, err := bootstrap.ValidateAccessConfiguration(*payload.AccessTeamDomain, *payload.AccessAudience)
		if err != nil {
			return ImportResult{}, err
		}
		payload.AccessTeamDomain = &team
		payload.AccessAudience = &audience
	}
	store, err := state.Open(ctx, payload.DatabasePath, payload.ExpectedUID)
	if err != nil {
		return ImportResult{}, err
	}
	closeStore := true
	defer func() {
		if closeStore {
			_ = store.Close()
		}
	}()
	installation, err := store.Installation(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	if installation.ID != payload.ExpectedInstallationID {
		return ImportResult{}, errors.New("restored installation ID differs from control manifest")
	}
	targets, err := store.BackupTargets(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	var recoveryTarget state.BackupTarget
	for _, candidate := range targets {
		if candidate.Endpoint == canonical.Endpoint && candidate.Region == canonical.Region &&
			candidate.Bucket == canonical.Bucket && candidate.Prefix == canonical.Prefix {
			recoveryTarget = candidate
			break
		}
	}
	if recoveryTarget.ID == "" {
		return ImportResult{}, errors.New("selected recovery storage is not present in the control snapshot")
	}
	encryptedSecret, err := backup.SealTargetSecret(master, installation.ID, canonical.SecretAccessKey)
	if err != nil {
		return ImportResult{}, err
	}
	if err := store.ImportControl(ctx, state.ControlImport{
		ExpectedInstallationID: installation.ID,
		AccessTeamDomain:       payload.AccessTeamDomain, AccessAudience: payload.AccessAudience,
		Target: state.BackupTarget{
			ID:       recoveryTarget.ID,
			Endpoint: canonical.Endpoint, Region: canonical.Region, Bucket: canonical.Bucket,
			Prefix: canonical.Prefix, AccessKeyID: canonical.AccessKeyID,
			SecretAccessKeyEncrypted: encryptedSecret,
		},
		AuditEventID: payload.AuditEventID, ImportedAtMillis: payload.ImportedAtMillis,
	}); err != nil {
		return ImportResult{}, err
	}
	installation, err = store.Installation(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	certificate, err := bootstrap.CertificateForHostname(installation.AdminHostname, installation.OriginCertificates)
	if err != nil {
		return ImportResult{}, err
	}
	if err := store.Checkpoint(ctx); err != nil {
		return ImportResult{}, err
	}
	if err := store.Close(); err != nil {
		return ImportResult{}, err
	}
	closeStore = false
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Lstat(payload.DatabasePath + suffix); err == nil && info.Size() != 0 {
			return ImportResult{}, errors.New("restore importer left non-empty SQLite sidecar")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return ImportResult{}, err
		}
	}
	return ImportResult{AdminHostname: installation.AdminHostname, OriginCertificatePEM: certificate}, nil
}

func NewImportPayload(databasePath string, manifest backup.ControlManifest, input ValidatedInput, importedAtMillis int64, expectedUID int, random io.Reader) (ImportPayload, error) {
	auditID, err := id.NewWith(time.UnixMilli(importedAtMillis), random)
	if err != nil {
		return ImportPayload{}, err
	}
	return ImportPayload{
		DatabasePath: databasePath, ExpectedInstallationID: manifest.InstallationID,
		ExpectedSchemaVersion: manifest.SchemaVersion, MasterRecoveryKey: masterkey.RecoveryString(input.Master),
		Remote: importTarget(input.Remote), AccessTeamDomain: input.AccessTeamDomain, AccessAudience: input.AccessAudience,
		AuditEventID: auditID, ImportedAtMillis: importedAtMillis, ExpectedUID: expectedUID,
	}, nil
}
