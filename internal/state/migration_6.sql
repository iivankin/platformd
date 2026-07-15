CREATE TABLE runtime_deployments (
  id TEXT PRIMARY KEY,
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('postgres', 'redis')),
  resource_id TEXT NOT NULL,
  image_tag TEXT NOT NULL,
  image_digest TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted', 'removed')),
  active INTEGER NOT NULL DEFAULT 0 CHECK (active IN (0, 1)),
  error_code TEXT,
  error_message TEXT,
  created_at INTEGER NOT NULL,
  finished_at INTEGER
) STRICT;

CREATE INDEX runtime_deployments_resource_created_idx
  ON runtime_deployments(resource_kind, resource_id, created_at DESC);
CREATE UNIQUE INDEX runtime_deployments_active_idx
  ON runtime_deployments(resource_kind, resource_id) WHERE active = 1;

INSERT INTO schema_migrations(version, applied_at)
VALUES (6, unixepoch('subsec') * 1000);

PRAGMA user_version = 6;
