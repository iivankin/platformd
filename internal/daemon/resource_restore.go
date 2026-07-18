package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type ordinaryVolumeRepository interface {
	Volume(context.Context, string) (state.Volume, error)
}

type ordinaryVolumeBackupConfig struct {
	Store ordinaryVolumeRepository
	Root  string
}

func resourceRestorers(
	runtime *runtimeStack,
	registryApplication *registry.Application,
	objectStoreApplication *objectstore.Application,
	volumeConfigs ...ordinaryVolumeBackupConfig,
) map[string]backup.ResourceRestorer {
	result := map[string]backup.ResourceRestorer{
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
		"registry": backup.ResourceRestorerFunc(func(
			ctx context.Context,
			request backup.ResourceRestoreRequest,
		) error {
			if err := requireConfirmedResourceReplacement(request.Options, "Registry"); err != nil {
				return err
			}
			if err := requireNoResourceAttachments(request.Source.Envelope); err != nil {
				return err
			}
			_, err := registryApplication.RestoreSnapshot(ctx, registry.RestoreInput{
				RepositoryID: request.ResourceID, Archive: request.Source.Reader,
				PolicyMode: registry.RestoreApplySnapshotPolicy,
				Actor: registry.Actor{
					Kind: request.Actor.Kind, ID: request.Actor.ID, Email: request.Actor.Email,
				},
			})
			return err
		}),
		"object_store": backup.ResourceRestorerFunc(func(
			ctx context.Context,
			request backup.ResourceRestoreRequest,
		) error {
			if err := requireConfirmedResourceReplacement(request.Options, "ObjectStore"); err != nil {
				return err
			}
			metadata, err := io.ReadAll(request.Source.Reader)
			if err != nil {
				return err
			}
			_, err = objectStoreApplication.RestoreSnapshot(ctx, objectstore.RestoreInput{
				StoreID: request.ResourceID, Metadata: metadata,
				ValidateAttachments: func(attachments []objectstore.BackupAttachment) error {
					descriptors := make([]backup.ResourceAttachment, len(attachments))
					for index, attachment := range attachments {
						descriptors[index] = backup.ResourceAttachment{
							Index: attachment.Index, Size: attachment.Size, SHA256: attachment.SHA256,
						}
					}
					return backup.ValidateResourceAttachments(request.Source.Envelope, descriptors)
				},
				OpenAttachment: func(_ context.Context, attachment objectstore.BackupAttachment) (io.ReadCloser, error) {
					return request.Source.OpenAttachment(backup.ResourceAttachment{
						Index: attachment.Index, Size: attachment.Size, SHA256: attachment.SHA256,
					})
				},
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
				return volume.RestoreBackup(ctx, config.Root, stored, request.Source.Reader)
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
