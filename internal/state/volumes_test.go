package state

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestVolumeLifecyclePersistsImmutableOwnerAndAudit(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	service := createVolumeTestService(t, store)

	created, err := store.CreateVolume(context.Background(), CreateVolume{
		Volume: Volume{
			ID: "volume", ProjectID: "project", ServiceID: service.ID,
			Name: "data", OwnerUID: 1000, OwnerGID: 1001, CreatedAtMillis: 2,
		},
		AuditEventID: "volume-create-audit", ActorKind: "access", ActorID: "subject",
		ActorEmail: "user@example.com", RequestCorrelationID: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.OwnerUID != 1000 || created.OwnerGID != 1001 {
		t.Fatalf("created volume = %+v", created)
	}
	volumes, err := store.VolumesByService(context.Background(), "project", service.ID)
	if err != nil || len(volumes) != 1 || volumes[0] != created {
		t.Fatalf("volumes = %+v, %v", volumes, err)
	}
	var action, targetKind, targetID, correlationID, metadata string
	if err := store.QueryRowContext(context.Background(), `
SELECT action, target_kind, target_id, request_correlation_id, metadata_json
FROM audit_events WHERE id = 'volume-create-audit'`).Scan(
		&action, &targetKind, &targetID, &correlationID, &metadata,
	); err != nil {
		t.Fatal(err)
	}
	if action != "volume.create" || targetKind != "volume" || targetID != created.ID ||
		correlationID != "request" ||
		metadata != `{"actorEmail":"user@example.com","ownerGid":"1001","ownerUid":"1000","serviceId":"service"}` {
		t.Fatalf("volume audit = %q/%q/%q/%q %s", action, targetKind, targetID, correlationID, metadata)
	}
}

func TestVolumeDeleteRejectsDesiredAndActiveReferences(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	service := createVolumeTestService(t, store)
	createVolumeTestVolume(t, store)

	withMount := service.Snapshot
	withMount.VolumeMounts = []serviceconfig.VolumeMount{{VolumeID: "volume", ContainerPath: "/data"}}
	updated, err := store.UpdateService(context.Background(), UpdateServiceInput{
		ID: service.ID, ProjectID: service.ProjectID, Enabled: true, Snapshot: withMount,
		ExpectedUpdatedMillis: service.UpdatedAtMillis, AuditEventID: "mount-audit",
		ActorKind: "access", ActorID: "subject", ActorEmail: "user@example.com", UpdatedAtMillis: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	deleteInput := DeleteVolume{
		ProjectID: "project", ServiceID: service.ID, VolumeID: "volume",
		AuditEventID: "delete-audit", ActorKind: "access", ActorID: "subject",
		ActorEmail: "user@example.com", DeletedAtMillis: 4,
	}
	if _, err := store.DeleteVolume(context.Background(), deleteInput); !errors.Is(err, ErrVolumeInUse) {
		t.Fatalf("delete desired reference error = %v", err)
	}

	withoutMount := updated.Snapshot
	withoutMount.VolumeMounts = nil
	updated, err = store.UpdateService(context.Background(), UpdateServiceInput{
		ID: service.ID, ProjectID: service.ProjectID, Enabled: true, Snapshot: withoutMount,
		ExpectedUpdatedMillis: updated.UpdatedAtMillis, AuditEventID: "unmount-audit",
		ActorKind: "access", ActorID: "subject", ActorEmail: "user@example.com", UpdatedAtMillis: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	activeSnapshot := withMount
	_, snapshotJSON, _, err := serviceconfig.Canonical(activeSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.ExecContext(context.Background(), `
INSERT INTO deployments(
  id, service_id, image_digest, image_reference, service_config_hash, snapshot_json,
  status, created_at, finished_at
) VALUES ('active', 'service', ?, 'docker.io/library/alpine:latest', 'config', ?, 'succeeded', 5, 5);
UPDATE services SET active_deployment_id = 'active' WHERE id = 'service'`,
		"sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4", string(snapshotJSON),
	); err != nil {
		t.Fatal(err)
	}
	deleteInput.AuditEventID = "active-delete-audit"
	deleteInput.DeletedAtMillis = 6
	if _, err := store.DeleteVolume(context.Background(), deleteInput); !errors.Is(err, ErrVolumeInUse) {
		t.Fatalf("delete active reference error = %v", err)
	}
	if _, err := store.database.ExecContext(context.Background(), `
UPDATE services SET active_deployment_id = NULL WHERE id = 'service'`); err != nil {
		t.Fatal(err)
	}
	deleteInput.AuditEventID = "successful-delete-audit"
	deleteInput.DeletedAtMillis = 7
	deleted, err := store.DeleteVolume(context.Background(), deleteInput)
	if err != nil || deleted.ID != "volume" {
		t.Fatalf("delete volume = %+v, %v", deleted, err)
	}
	var count int
	if err := store.QueryRowContext(context.Background(), `
SELECT count(*) FROM audit_events WHERE id IN ('delete-audit', 'active-delete-audit')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed volume deletion committed an audit event")
	}
}

func TestCreateVolumeRejectsDuplicateNameAndInvalidOwner(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	createVolumeTestService(t, store)
	createVolumeTestVolume(t, store)

	_, err := store.CreateVolume(context.Background(), CreateVolume{
		Volume: Volume{
			ID: "duplicate", ProjectID: "project", ServiceID: "service",
			Name: "data", OwnerUID: 1, OwnerGID: 1, CreatedAtMillis: 3,
		},
		AuditEventID: "duplicate-audit", ActorKind: "access", ActorID: "subject",
		ActorEmail: "user@example.com",
	})
	if !errors.Is(err, ErrVolumeNameConflict) {
		t.Fatalf("duplicate name error = %v", err)
	}
	_, err = store.CreateVolume(context.Background(), CreateVolume{
		Volume: Volume{
			ID: "invalid", ProjectID: "project", ServiceID: "service",
			Name: "other", OwnerUID: -1, OwnerGID: 1, CreatedAtMillis: 3,
		},
		AuditEventID: "invalid-audit", ActorKind: "access", ActorID: "subject",
		ActorEmail: "user@example.com",
	})
	if err == nil {
		t.Fatal("negative volume owner was accepted")
	}
}

func createVolumeTestService(t *testing.T, store *Store) ServiceDesired {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateProject(ctx, CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit",
		ActorID: "subject", ActorEmail: "user@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := store.CreateService(ctx, CreateService{
		ID: "service", ProjectID: "project", Name: "web", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("example/image:latest")},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "subject",
		ActorEmail: "user@example.com", CreatedAtMillis: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func createVolumeTestVolume(t *testing.T, store *Store) Volume {
	t.Helper()
	volume, err := store.CreateVolume(context.Background(), CreateVolume{
		Volume: Volume{
			ID: "volume", ProjectID: "project", ServiceID: "service",
			Name: "data", OwnerUID: 1000, OwnerGID: 1000, CreatedAtMillis: 2,
		},
		AuditEventID: "volume-audit", ActorKind: "access", ActorID: "subject",
		ActorEmail: "user@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	return volume
}
