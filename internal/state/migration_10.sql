CREATE TABLE backup_targets (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  endpoint TEXT NOT NULL,
  region TEXT NOT NULL,
  bucket TEXT NOT NULL,
  prefix TEXT NOT NULL,
  access_key_id TEXT NOT NULL,
  secret_access_key_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (endpoint, region, bucket, prefix)
) STRICT;

INSERT INTO backup_targets(
  id, name, endpoint, region, bucket, prefix, access_key_id,
  secret_access_key_encrypted, created_at, updated_at
)
SELECT 'primary', 'Primary storage', endpoint, region, bucket, prefix,
       access_key_id, secret_access_key_encrypted, created_at, updated_at
FROM backup_target;

DROP TABLE backup_target;

ALTER TABLE installation
  ADD COLUMN backup_control_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT;

UPDATE installation
SET backup_control_target_id = 'primary'
WHERE EXISTS (SELECT 1 FROM backup_targets WHERE id = 'primary');

ALTER TABLE registry_repositories
  ADD COLUMN backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT;
ALTER TABLE object_stores
  ADD COLUMN backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT;
ALTER TABLE managed_postgres
  ADD COLUMN backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT;
ALTER TABLE managed_redis
  ADD COLUMN backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT;

UPDATE registry_repositories SET backup_target_id = 'primary'
WHERE EXISTS (SELECT 1 FROM backup_targets WHERE id = 'primary');
UPDATE object_stores SET backup_target_id = 'primary'
WHERE EXISTS (SELECT 1 FROM backup_targets WHERE id = 'primary');
UPDATE managed_postgres SET backup_target_id = 'primary'
WHERE EXISTS (SELECT 1 FROM backup_targets WHERE id = 'primary');
UPDATE managed_redis SET backup_target_id = 'primary'
WHERE EXISTS (SELECT 1 FROM backup_targets WHERE id = 'primary');

ALTER TABLE volumes ADD COLUMN backup_enabled INTEGER NOT NULL DEFAULT 0
  CHECK (backup_enabled IN (0, 1));
ALTER TABLE volumes ADD COLUMN backup_cron TEXT;
ALTER TABLE volumes ADD COLUMN backup_retention_count INTEGER NOT NULL DEFAULT 7
  CHECK (backup_retention_count BETWEEN 1 AND 100);
ALTER TABLE volumes
  ADD COLUMN backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT;
ALTER TABLE volumes ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;
UPDATE volumes SET updated_at = created_at;

ALTER TABLE backups RENAME TO backups_v9;

CREATE TABLE backups (
  id TEXT PRIMARY KEY,
  target_id TEXT NOT NULL,
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('control', 'registry', 'object_store', 'postgres', 'redis', 'volume')),
  resource_id TEXT NOT NULL,
  scheduled_occurrence INTEGER,
  generation_id TEXT,
  status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
  size_bytes INTEGER CHECK (size_bytes >= 0),
  error_code TEXT,
  error_message TEXT,
  started_at INTEGER NOT NULL,
  finished_at INTEGER
) STRICT;

INSERT INTO backups(
  id, target_id, resource_kind, resource_id, scheduled_occurrence,
  generation_id, status, size_bytes, error_code, error_message,
  started_at, finished_at
)
SELECT id,
       CASE WHEN EXISTS (SELECT 1 FROM backup_targets WHERE id = 'primary')
         THEN 'primary' ELSE 'removed' END,
       resource_kind, resource_id, scheduled_occurrence, generation_id,
       status, size_bytes, error_code, error_message, started_at, finished_at
FROM backups_v9;

DROP TABLE backups_v9;

CREATE INDEX backups_resource_started_idx
  ON backups(target_id, resource_kind, resource_id, started_at DESC);
CREATE UNIQUE INDEX backups_scheduled_occurrence_idx
  ON backups(resource_kind, resource_id, scheduled_occurrence)
  WHERE scheduled_occurrence IS NOT NULL;

INSERT INTO schema_migrations(version, applied_at)
VALUES (10, unixepoch('subsec') * 1000);

PRAGMA user_version = 10;
