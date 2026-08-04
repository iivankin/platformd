package state

import (
	"context"
	"testing"
)

func TestImportControlClearsPayloadMetadataButPreservesResourceConfiguration(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateInstallation(ctx, InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("sealed"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO services(
  id, project_id, name, source_json, environment_json, enabled, created_at, updated_at
) VALUES ('service', 'project', 'api', '{"type":"public_image","image":{"reference":"alpine:3.22"}}', '{}', 1, 1, 1);
INSERT INTO volumes(
  id, project_id, service_id, name, created_at, updated_at
) VALUES ('volume', 'project', 'service', 'data', 1, 1);
INSERT INTO volume_initializations(volume_id, initialized_at)
VALUES ('volume', 1);
INSERT INTO object_stores(
  id, project_id, name, bucket_name, created_at, updated_at
) VALUES ('store', 'project', 'assets', 'assets', 1, 1);
INSERT INTO s3_credentials(
  id, object_store_id, name, permission, secret_encrypted, created_at
) VALUES ('s3-credential', 'store', 'robot', 'read_write', x'01', 1);
INSERT INTO service_image_uploads(
  id, service_id, tag, expected_length, received_length, expected_sha256,
  temporary_path, oidc_metadata_json, status, created_at, updated_at, expires_at
) VALUES ('upload', 'service', 'latest', 10, 0,
  '3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4',
  '/tmp/upload', '{}', 'uploading', 1, 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetBackupTarget(ctx, SetBackupTarget{
		Target: BackupTarget{
			ID: "target", Name: "Primary", Endpoint: "https://s3.example.com", Region: "us-east-1",
			Bucket: "backup", AccessKeyID: "old-access", SecretAccessKeyEncrypted: []byte("old-secret"),
		},
		AuditEventID: "target-audit", ActorKind: "access", ActorID: "user",
		ActorEmail: "admin@example.com", UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.ImportControl(ctx, ControlImport{
		ExpectedInstallationID: "installation",
		Target: BackupTarget{
			ID:       "target",
			Endpoint: "https://s3.example.com", Region: "us-east-1", Bucket: "backup",
			AccessKeyID: "access", SecretAccessKeyEncrypted: []byte("sealed-secret"),
		},
		AuditEventID: "restore-audit", ImportedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"service_image_uploads",
		"volume_initializations",
	} {
		var count int
		if err := store.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s still contains %d recovery-stale rows", table, count)
		}
	}
	for _, table := range []string{
		"object_stores", "s3_credentials", "volumes",
	} {
		var count int
		if err := store.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s configuration count = %d", table, count)
		}
	}
}
