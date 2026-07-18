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
INSERT INTO object_stores(
  id, project_id, name, bucket_name, created_at, updated_at
) VALUES ('store', 'project', 'assets', 'assets', 1, 1);
INSERT INTO s3_credentials(
  id, object_store_id, name, permission, secret_encrypted, created_at
) VALUES ('s3-credential', 'store', 'robot', 'read_write', x'01', 1);
INSERT INTO object_payloads(
  id, object_store_id, plaintext_size, chunk_count, plaintext_sha256, created_at
) VALUES (
  'payload', 'store', 1, 1,
  'ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb', 1
);
INSERT INTO objects(
  object_store_id, object_key, payload_id, etag, size, created_at, updated_at
) VALUES ('store', 'key', 'payload', 'etag', 1, 1, 1);
INSERT INTO multipart_uploads(
  id, object_store_id, object_key, created_at, expires_at
) VALUES ('multipart', 'store', 'pending', 1, 2);
INSERT INTO multipart_parts(
  upload_id, part_number, plaintext_size, checksum_sha256, chunk_count
) VALUES (
  'multipart', 1, 1,
  'ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb', 1
);
INSERT INTO registry_repositories(
  id, name, public_pull, created_at, updated_at
) VALUES ('repository', 'acme/app', 1, 1, 1);
INSERT INTO registry_credentials(
  id, repository_id, name, permission, secret_hmac, created_at
) VALUES ('registry-credential', 'repository', 'robot', 'pull_push', x'01', 1);
INSERT INTO registry_manifests(
  repository_id, digest, media_type, body, pushed_at
) VALUES (
  'repository',
  'sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4',
  'application/vnd.oci.image.manifest.v1+json', x'7b7d', 1
);
INSERT INTO registry_tags(
  repository_id, name, manifest_digest, updated_at
) VALUES (
  'repository', 'latest',
  'sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4', 1
);
INSERT INTO registry_uploads(
  id, repository_id, credential_id, created_at, updated_at, expires_at
) VALUES ('upload', 'repository', 'registry-credential', 1, 1, 2)`); err != nil {
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
		"registry_uploads", "registry_tags", "registry_manifests",
		"multipart_parts", "multipart_uploads", "objects", "object_payloads",
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
		"registry_repositories", "registry_credentials", "object_stores", "s3_credentials",
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
