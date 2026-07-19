CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
) STRICT;

CREATE TABLE installation (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  id TEXT NOT NULL UNIQUE,
  admin_hostname TEXT NOT NULL UNIQUE,
  automation_hostname TEXT UNIQUE,
  registry_hostname TEXT UNIQUE,
  access_team_domain TEXT NOT NULL,
  access_audience TEXT NOT NULL,
  console_passphrase_phc TEXT NOT NULL,
  recovery_mode INTEGER NOT NULL DEFAULT 0 CHECK (recovery_mode IN (0, 1)),
  backup_control_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE origin_certificates (
  id TEXT PRIMARY KEY,
  certificate_pem TEXT NOT NULL,
  private_key_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE github_app_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  app_id INTEGER NOT NULL CHECK (app_id > 0),
  app_slug TEXT NOT NULL,
  private_key_encrypted BLOB NOT NULL,
  webhook_secret_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE cloudflare_dns_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  api_token_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE registry_repositories (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  public_pull INTEGER NOT NULL DEFAULT 0 CHECK (public_pull IN (0, 1)),
  backup_enabled INTEGER NOT NULL DEFAULT 0 CHECK (backup_enabled IN (0, 1)),
  backup_cron TEXT,
  backup_retention_count INTEGER NOT NULL DEFAULT 7 CHECK (backup_retention_count BETWEEN 1 AND 100),
  backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE registry_credentials (
  id TEXT PRIMARY KEY,
  repository_id TEXT NOT NULL REFERENCES registry_repositories(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  permission TEXT NOT NULL CHECK (permission IN ('pull', 'pull_push')),
  secret_hmac BLOB NOT NULL,
  secret_encrypted BLOB,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  UNIQUE (repository_id, name)
) STRICT;

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

CREATE TABLE secrets (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  value_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE (project_id, name)
) STRICT;

CREATE TABLE services (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  source_json TEXT NOT NULL CHECK (json_valid(source_json)),
  command_json TEXT CHECK (command_json IS NULL OR json_valid(command_json)),
  args_json TEXT CHECK (args_json IS NULL OR json_valid(args_json)),
  environment_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(environment_json)),
  health_port INTEGER CHECK (health_port BETWEEN 1 AND 65535),
  health_path TEXT,
  health_timeout_seconds INTEGER NOT NULL DEFAULT 60 CHECK (health_timeout_seconds BETWEEN 1 AND 3600),
  cpu_millis INTEGER CHECK (cpu_millis > 0),
  memory_bytes INTEGER CHECK (memory_bytes > 0),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  active_deployment_id TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY (active_deployment_id) REFERENCES deployments(id) DEFERRABLE INITIALLY DEFERRED,
  UNIQUE (project_id, name)
) STRICT;

CREATE TABLE service_image_credentials (
  service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  registry_host TEXT NOT NULL,
  username TEXT NOT NULL,
  password_encrypted BLOB NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE service_secret_refs (
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  environment_name TEXT NOT NULL,
  secret_id TEXT NOT NULL REFERENCES secrets(id) ON DELETE RESTRICT,
  PRIMARY KEY (service_id, environment_name)
) WITHOUT ROWID, STRICT;

CREATE TABLE volumes (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  owner_uid INTEGER NOT NULL CHECK (owner_uid >= 0),
  owner_gid INTEGER NOT NULL CHECK (owner_gid >= 0),
  backup_enabled INTEGER NOT NULL DEFAULT 0 CHECK (backup_enabled IN (0, 1)),
  backup_cron TEXT,
  backup_retention_count INTEGER NOT NULL DEFAULT 7 CHECK (backup_retention_count BETWEEN 1 AND 100),
  backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (service_id, name)
) STRICT;

CREATE TABLE service_volume_mounts (
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  volume_id TEXT NOT NULL REFERENCES volumes(id) ON DELETE RESTRICT,
  container_path TEXT NOT NULL,
  PRIMARY KEY (service_id, container_path),
  UNIQUE (volume_id)
) WITHOUT ROWID, STRICT;

CREATE TABLE deployments (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  image_digest TEXT NOT NULL,
  image_reference TEXT NOT NULL,
  source_revision TEXT,
  source_commit_message TEXT,
  service_config_hash TEXT NOT NULL,
  snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)),
  status TEXT NOT NULL CHECK (status IN ('waiting', 'running', 'succeeded', 'failed', 'interrupted', 'skipped')),
  error_code TEXT,
  error_message TEXT,
  created_at INTEGER NOT NULL,
  finished_at INTEGER
) STRICT;

CREATE INDEX deployments_service_created_idx ON deployments(service_id, created_at DESC);
CREATE INDEX deployments_retry_pair_idx ON deployments(service_id, service_config_hash, image_digest, created_at DESC);

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

CREATE TABLE service_domains (
  hostname TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  target_port INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
  created_at INTEGER NOT NULL
) WITHOUT ROWID, STRICT;

CREATE TABLE service_listeners (
  protocol TEXT NOT NULL CHECK (protocol IN ('tcp', 'udp')),
  public_port INTEGER NOT NULL CHECK (public_port BETWEEN 1 AND 65535),
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  target_port INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (protocol, public_port)
) WITHOUT ROWID, STRICT;

CREATE INDEX service_listeners_service_idx
  ON service_listeners(service_id, protocol, public_port);

CREATE TABLE object_stores (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  bucket_name TEXT NOT NULL,
  public_hostname TEXT UNIQUE,
  cors_origins_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(cors_origins_json)),
  backup_enabled INTEGER NOT NULL DEFAULT 0 CHECK (backup_enabled IN (0, 1)),
  backup_cron TEXT,
  backup_retention_count INTEGER NOT NULL DEFAULT 7 CHECK (backup_retention_count BETWEEN 1 AND 100),
  backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, name),
  UNIQUE (project_id, bucket_name)
) STRICT;

CREATE TABLE s3_credentials (
  id TEXT PRIMARY KEY,
  object_store_id TEXT NOT NULL REFERENCES object_stores(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  permission TEXT NOT NULL CHECK (permission IN ('read', 'read_write')),
  secret_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  UNIQUE (object_store_id, name)
) STRICT;

CREATE TABLE object_payloads (
  id TEXT PRIMARY KEY,
  object_store_id TEXT NOT NULL REFERENCES object_stores(id) ON DELETE CASCADE,
  plaintext_size INTEGER NOT NULL CHECK (plaintext_size >= 0),
  chunk_count INTEGER NOT NULL CHECK (chunk_count >= 0),
  plaintext_sha256 TEXT NOT NULL,
  created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX object_payloads_store_idx ON object_payloads(object_store_id);

CREATE TABLE objects (
  object_store_id TEXT NOT NULL REFERENCES object_stores(id) ON DELETE CASCADE,
  object_key TEXT NOT NULL,
  payload_id TEXT NOT NULL REFERENCES object_payloads(id) ON DELETE RESTRICT,
  content_type TEXT,
  etag TEXT NOT NULL,
  size INTEGER NOT NULL CHECK (size >= 0),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (object_store_id, object_key)
) WITHOUT ROWID, STRICT;

CREATE TABLE multipart_uploads (
  id TEXT PRIMARY KEY,
  object_store_id TEXT NOT NULL REFERENCES object_stores(id) ON DELETE CASCADE,
  object_key TEXT NOT NULL,
  content_type TEXT,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE TABLE multipart_parts (
  upload_id TEXT NOT NULL REFERENCES multipart_uploads(id) ON DELETE CASCADE,
  part_number INTEGER NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
  plaintext_size INTEGER NOT NULL CHECK (plaintext_size >= 0),
  checksum_sha256 TEXT NOT NULL,
  chunk_count INTEGER NOT NULL CHECK (chunk_count >= 0),
  PRIMARY KEY (upload_id, part_number)
) WITHOUT ROWID, STRICT;

CREATE TABLE managed_postgres (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  image_tag TEXT NOT NULL,
  image_digest TEXT NOT NULL,
  volume_id TEXT NOT NULL UNIQUE,
  database_name TEXT NOT NULL,
  owner_username TEXT NOT NULL,
  owner_password_encrypted BLOB NOT NULL,
  bootstrap_password_encrypted BLOB NOT NULL,
  cpu_millis INTEGER CHECK (cpu_millis > 0),
  memory_bytes INTEGER CHECK (memory_bytes > 0),
  backup_enabled INTEGER NOT NULL DEFAULT 0 CHECK (backup_enabled IN (0, 1)),
  backup_cron TEXT,
  backup_retention_count INTEGER NOT NULL DEFAULT 7 CHECK (backup_retention_count BETWEEN 1 AND 100),
  backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, name)
) STRICT;

CREATE TABLE managed_postgres_extensions (
  postgres_id TEXT NOT NULL REFERENCES managed_postgres(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  version TEXT NOT NULL,
  recipe_digest TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (postgres_id, name)
) WITHOUT ROWID, STRICT;

CREATE TABLE managed_redis (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  image_tag TEXT NOT NULL,
  image_digest TEXT NOT NULL,
  volume_id TEXT NOT NULL UNIQUE,
  password_encrypted BLOB NOT NULL,
  cpu_millis INTEGER CHECK (cpu_millis > 0),
  memory_bytes INTEGER CHECK (memory_bytes > 0),
  backup_enabled INTEGER NOT NULL DEFAULT 0 CHECK (backup_enabled IN (0, 1)),
  backup_cron TEXT,
  backup_retention_count INTEGER NOT NULL DEFAULT 7 CHECK (backup_retention_count BETWEEN 1 AND 100),
  backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, name)
) STRICT;

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

CREATE INDEX backups_resource_started_idx ON backups(target_id, resource_kind, resource_id, started_at DESC);
CREATE UNIQUE INDEX backups_scheduled_occurrence_idx ON backups(resource_kind, resource_id, scheduled_occurrence) WHERE scheduled_occurrence IS NOT NULL;

CREATE TABLE api_tokens (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('read', 'admin')),
  project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
  secret_hmac BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  revoked_at INTEGER
) STRICT;

CREATE TABLE operations (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
  progress TEXT,
  error_code TEXT,
  error_message TEXT,
  started_at INTEGER NOT NULL,
  finished_at INTEGER
) STRICT;

CREATE INDEX operations_started_idx ON operations(started_at DESC);

CREATE TABLE audit_events (
  id TEXT PRIMARY KEY,
  actor_kind TEXT NOT NULL CHECK (actor_kind IN ('access', 'token', 'system', 'local_root')),
  actor_id TEXT NOT NULL,
  action TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  request_correlation_id TEXT,
  result TEXT NOT NULL CHECK (result IN ('succeeded', 'failed')),
  metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata_json)),
  created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX audit_events_created_idx ON audit_events(created_at DESC);

CREATE TABLE resource_metric_samples (
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('service', 'postgres', 'redis')),
  resource_id TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  cpu_usage_micros INTEGER NOT NULL CHECK (cpu_usage_micros >= 0),
  memory_bytes INTEGER NOT NULL CHECK (memory_bytes >= 0),
  network_rx_bytes INTEGER CHECK (network_rx_bytes IS NULL OR network_rx_bytes >= 0),
  network_tx_bytes INTEGER CHECK (network_tx_bytes IS NULL OR network_tx_bytes >= 0),
  running INTEGER NOT NULL CHECK (running IN (0, 1)),
  PRIMARY KEY (resource_kind, resource_id, observed_at)
) WITHOUT ROWID, STRICT;

CREATE INDEX resource_metric_samples_retention_idx
  ON resource_metric_samples(observed_at);

INSERT INTO schema_migrations(version, applied_at)
VALUES (13, unixepoch('subsec') * 1000);

PRAGMA user_version = 13;
