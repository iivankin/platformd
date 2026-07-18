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

CREATE INDEX backups_resource_started_idx
  ON backups(target_id, resource_kind, resource_id, started_at DESC);
CREATE UNIQUE INDEX backups_scheduled_occurrence_idx
  ON backups(resource_kind, resource_id, scheduled_occurrence)
  WHERE scheduled_occurrence IS NOT NULL;

INSERT INTO schema_migrations(version, applied_at)
VALUES (10, unixepoch('subsec') * 1000);

PRAGMA user_version = 10;
