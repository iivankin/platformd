package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type ordinaryVolumeRepository interface {
	Volume(context.Context, string) (state.Volume, error)
	RecordVolumeInitialization(context.Context, string, string, string, int64) error
}

type ordinaryVolumeBackupConfig struct {
	Store ordinaryVolumeRepository
	Root  string
}

type imageRevisionRepository interface {
	ImageRevision(context.Context, string) (state.ImageRevision, error)
}

func resourceRestorers(
	runtime *runtimeStack,
	images imageRevisionRepository,
	objectStoreApplication *objectstore.Application,
	volumeConfigs ...ordinaryVolumeBackupConfig,
) map[string]backup.ResourceRestorer {
	result := map[string]backup.ResourceRestorer{
		"image": backup.ResourceRestorerFunc(func(ctx context.Context, request backup.ResourceRestoreRequest) error {
			if err := requireConfirmedResourceReplacement(request.Options, "Image"); err != nil {
				return err
			}
			if err := requireNoResourceAttachments(request.Source.Envelope); err != nil {
				return err
			}
			if images == nil {
				return errors.New("image revision store is unavailable")
			}
			revision, err := images.ImageRevision(ctx, request.ResourceID)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(revision.ArchivePath), 0o700); err != nil {
				return err
			}
			temporary := revision.ArchivePath + ".restore"
			file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			hash := sha256.New()
			_, copyErr := io.Copy(io.MultiWriter(file, hash), request.Source.Reader)
			closeErr := errors.Join(file.Sync(), file.Close())
			if err := errors.Join(copyErr, closeErr); err != nil {
				_ = os.Remove(temporary)
				return err
			}
			if hex.EncodeToString(hash.Sum(nil)) != revision.ArchiveSHA256 {
				_ = os.Remove(temporary)
				return errors.New("restored image archive SHA-256 mismatch")
			}
			if err := os.Rename(temporary, revision.ArchivePath); err != nil {
				_ = os.Remove(temporary)
				return err
			}
			return nil
		}),
		"postgres": backup.ResourceRestorerFunc(func(
			ctx context.Context,
			request backup.ResourceRestoreRequest,
		) error {
			if request.Options.Mode != "replace" || !request.Options.DestructiveConfirmed ||
				request.Options.NewResourceName != "" {
				return errors.New("managed PostgreSQL restore requires confirmed replace mode")
			}
			if err := requireNoResourceAttachments(request.Source.Envelope); err != nil {
				return err
			}
			return runtime.RestoreManagedPostgres(ctx, request.ResourceID, request.Source.Reader,
				managedpostgres.Actor{
					Kind: request.Actor.Kind, ID: request.Actor.ID, Email: request.Actor.Email,
				})
		}),
		"redis": backup.ResourceRestorerFunc(func(
			ctx context.Context,
			request backup.ResourceRestoreRequest,
		) error {
			if request.Options.Mode != "replace" || !request.Options.DestructiveConfirmed ||
				request.Options.NewResourceName != "" {
				return errors.New("managed Redis restore requires confirmed replace mode")
			}
			if err := requireNoResourceAttachments(request.Source.Envelope); err != nil {
				return err
			}
			return runtime.RestoreManagedRedis(ctx, request.ResourceID, request.Source.Reader,
				managedredis.Actor{
					Kind: request.Actor.Kind, ID: request.Actor.ID, Email: request.Actor.Email,
				})
		}),
		"object_store": backup.ResourceRestorerFunc(func(
			ctx context.Context,
			request backup.ResourceRestoreRequest,
		) error {
			if err := requireConfirmedResourceReplacement(request.Options, "ObjectStore"); err != nil {
				return err
			}
			if err := requireNoResourceAttachments(request.Source.Envelope); err != nil {
				return err
			}
			_, err := objectStoreApplication.RestoreSnapshot(ctx, objectstore.RestoreInput{
				StoreID: request.ResourceID, Archive: request.Source.Reader,
				Actor: objectstore.Actor{
					Kind: request.Actor.Kind, ID: request.Actor.ID, Email: request.Actor.Email,
				},
			})
			return err
		}),
	}
	if len(volumeConfigs) == 1 && volumeConfigs[0].Store != nil && volumeConfigs[0].Root != "" {
		config := volumeConfigs[0]
		result["volume"] = backup.ResourceRestorerFunc(func(
			ctx context.Context,
			request backup.ResourceRestoreRequest,
		) error {
			if err := requireConfirmedResourceReplacement(request.Options, "Volume"); err != nil {
				return err
			}
			if err := requireNoResourceAttachments(request.Source.Envelope); err != nil {
				return err
			}
			stored, err := config.Store.Volume(ctx, request.ResourceID)
			if err != nil {
				return err
			}
			return runtime.WithServiceQuiesced(ctx, stored.ServiceID, func() error {
				if err := volume.RestoreBackup(ctx, config.Root, stored, request.Source.Reader); err != nil {
					return err
				}
				return config.Store.RecordVolumeInitialization(
					ctx, stored.ProjectID, stored.ServiceID, stored.ID, time.Now().UnixMilli(),
				)
			})
		})
	}
	return result
}

func requireNoResourceAttachments(envelope backup.ResourceEnvelope) error {
	if envelope.AttachmentCount != 0 || envelope.AttachmentSize != 0 || envelope.AttachmentRoot != "" {
		return errors.New("resource generation contains unexpected attachments")
	}
	return nil
}

func requireConfirmedResourceReplacement(options backup.ResourceRestoreOptions, resource string) error {
	if options.Mode != "replace" || !options.DestructiveConfirmed || options.NewResourceName != "" {
		return fmt.Errorf("%s restore requires confirmed replace mode", resource)
	}
	return nil
}
