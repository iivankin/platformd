ALTER TABLE installation ADD COLUMN registry_hostname TEXT;
CREATE UNIQUE INDEX installation_registry_hostname_idx
  ON installation(registry_hostname) WHERE registry_hostname IS NOT NULL;

CREATE TABLE registry_manifests (
  repository_id TEXT NOT NULL REFERENCES registry_repositories(id) ON DELETE CASCADE,
  digest TEXT NOT NULL,
  media_type TEXT NOT NULL,
  body BLOB NOT NULL,
  pushed_at INTEGER NOT NULL,
  PRIMARY KEY (repository_id, digest)
) WITHOUT ROWID, STRICT;

CREATE TABLE registry_tags (
  repository_id TEXT NOT NULL,
  name TEXT NOT NULL,
  manifest_digest TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (repository_id, name),
  FOREIGN KEY (repository_id, manifest_digest)
    REFERENCES registry_manifests(repository_id, digest) ON DELETE CASCADE
) WITHOUT ROWID, STRICT;

CREATE INDEX registry_tags_manifest_idx ON registry_tags(repository_id, manifest_digest);

CREATE TABLE registry_uploads (
  id TEXT PRIMARY KEY,
  repository_id TEXT NOT NULL REFERENCES registry_repositories(id) ON DELETE CASCADE,
  credential_id TEXT NOT NULL REFERENCES registry_credentials(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX registry_uploads_repository_idx ON registry_uploads(repository_id, created_at);
CREATE INDEX registry_uploads_credential_idx ON registry_uploads(credential_id, created_at);
CREATE INDEX registry_uploads_expiry_idx ON registry_uploads(expires_at, id);

INSERT INTO schema_migrations(version, applied_at)
VALUES (2, unixepoch('subsec') * 1000);

PRAGMA user_version = 2;
