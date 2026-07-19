CREATE TABLE cloudflare_dns_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  api_token_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE preview_deployments (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  pull_request_number INTEGER NOT NULL CHECK (pull_request_number > 0),
  source_revision TEXT NOT NULL,
  source_commit_message TEXT,
  hostname TEXT NOT NULL,
  target_port INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
  image_digest TEXT NOT NULL,
  image_reference TEXT NOT NULL,
  service_config_hash TEXT NOT NULL,
  snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)),
  status TEXT NOT NULL CHECK (status IN ('building', 'active', 'failed', 'skipped', 'stopped', 'interrupted')),
  error_code TEXT,
  error_message TEXT,
  github_deployment_id INTEGER CHECK (github_deployment_id IS NULL OR github_deployment_id > 0),
  github_comment_id INTEGER CHECK (github_comment_id IS NULL OR github_comment_id > 0),
  cloudflare_records_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(cloudflare_records_json)),
  created_at INTEGER NOT NULL,
  finished_at INTEGER,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX preview_deployments_service_created_idx
  ON preview_deployments(service_id, created_at DESC);
CREATE INDEX preview_deployments_expiry_idx
  ON preview_deployments(expires_at, id);
CREATE UNIQUE INDEX preview_deployments_active_pr_idx
  ON preview_deployments(service_id, pull_request_number) WHERE status = 'active';
CREATE UNIQUE INDEX preview_deployments_active_hostname_idx
  ON preview_deployments(hostname) WHERE status = 'active';

INSERT INTO schema_migrations(version, applied_at)
VALUES (12, unixepoch('subsec') * 1000);

PRAGMA user_version = 12;
