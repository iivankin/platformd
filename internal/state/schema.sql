CREATE TABLE installation (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  id TEXT NOT NULL UNIQUE,
  admin_hostname TEXT NOT NULL UNIQUE,
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

CREATE TABLE cloudflare_dns_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  api_token_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE cloudflare_mesh_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  account_id TEXT NOT NULL,
  api_token_encrypted BLOB NOT NULL,
  node_id TEXT NOT NULL,
  node_name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  icon_bytes BLOB CHECK (icon_bytes IS NULL OR length(icon_bytes) BETWEEN 1 AND 131072),
  icon_content_type TEXT CHECK (
    icon_content_type IS NULL
    OR icon_content_type IN ('image/png', 'image/jpeg', 'image/webp')
  ),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE project_webhooks (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  event_types_json TEXT NOT NULL CHECK (json_valid(event_types_json) AND json_type(event_types_json) = 'array'),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX project_webhooks_project_idx ON project_webhooks(project_id, created_at, id);

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
  before_deploy_json TEXT CHECK (before_deploy_json IS NULL OR json_valid(before_deploy_json)),
  port_forward_json TEXT CHECK (port_forward_json IS NULL OR json_valid(port_forward_json)),
  health_port INTEGER CHECK (health_port BETWEEN 1 AND 65535),
  health_path TEXT,
  health_timeout_seconds INTEGER NOT NULL DEFAULT 60 CHECK (health_timeout_seconds BETWEEN 1 AND 3600),
  cpu_millis INTEGER CHECK (cpu_millis > 0),
  memory_bytes INTEGER CHECK (memory_bytes > 0),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  active_deployment_id TEXT,
  sentry_public_hostname TEXT,
  sentry_tunnel_path TEXT CHECK (
    sentry_tunnel_path IS NULL OR length(sentry_tunnel_path) BETWEEN 2 AND 256
  ),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY (active_deployment_id) REFERENCES deployments(id) DEFERRABLE INITIALLY DEFERRED,
  UNIQUE (project_id, name)
) STRICT;

CREATE UNIQUE INDEX services_sentry_public_hostname_idx
ON services(sentry_public_hostname) WHERE sentry_public_hostname IS NOT NULL;

CREATE TABLE service_image_credentials (
  service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  registry_host TEXT NOT NULL,
  username TEXT NOT NULL,
  password_encrypted BLOB NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE service_telemetry_credentials (
  service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  artifact_token_sha256 BLOB NOT NULL CHECK (length(artifact_token_sha256) = 32),
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE service_telemetry_webhooks (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  event_types_json TEXT NOT NULL CHECK (json_valid(event_types_json) AND json_type(event_types_json) = 'array'),
  secret_encrypted BLOB NOT NULL,
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX service_telemetry_webhooks_service_idx
ON service_telemetry_webhooks(service_id, created_at, id);

CREATE TABLE metric_charts (
  id TEXT PRIMARY KEY,
  scope_kind TEXT NOT NULL CHECK (scope_kind IN ('installation', 'project', 'service')),
  project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
  service_id TEXT REFERENCES services(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
  sql TEXT NOT NULL CHECK (length(sql) BETWEEN 1 AND 16384),
  visualization TEXT NOT NULL DEFAULT 'area' CHECK (visualization IN ('line', 'area', 'bar', 'value')),
  legend TEXT NOT NULL DEFAULT '' CHECK (length(legend) <= 80),
  unit TEXT CHECK (unit IS NULL OR length(unit) <= 32),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  CHECK (
    (scope_kind = 'installation' AND project_id IS NULL AND service_id IS NULL) OR
    (scope_kind = 'project' AND project_id IS NOT NULL AND service_id IS NULL) OR
    (scope_kind = 'service' AND project_id IS NULL AND service_id IS NOT NULL)
  )
) STRICT;

CREATE INDEX metric_charts_scope_idx
ON metric_charts(scope_kind, project_id, service_id, created_at, id);

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

CREATE TABLE volume_initializations (
  volume_id TEXT PRIMARY KEY REFERENCES volumes(id) ON DELETE CASCADE,
  initialized_at INTEGER NOT NULL
) STRICT;

CREATE TABLE deployments (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  image_digest TEXT NOT NULL,
  image_reference TEXT NOT NULL,
  image_revision_id TEXT,
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

CREATE TABLE service_image_revisions (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('production', 'preview')),
  archive_path TEXT NOT NULL UNIQUE,
  archive_sha256 TEXT NOT NULL,
  image_digest TEXT,
  deployment_id TEXT REFERENCES deployments(id) ON DELETE SET NULL,
  preview_id TEXT,
  oidc_metadata_json TEXT NOT NULL CHECK (json_valid(oidc_metadata_json)),
  status TEXT NOT NULL CHECK (status IN ('importing', 'active', 'retired', 'failed')),
  created_at INTEGER NOT NULL,
  activated_at INTEGER,
  retired_at INTEGER,
  expires_at INTEGER
) STRICT;

CREATE INDEX service_image_revisions_service_created_idx
  ON service_image_revisions(service_id, created_at DESC);
CREATE INDEX service_image_revisions_gc_idx
  ON service_image_revisions(status, retired_at, expires_at);
CREATE UNIQUE INDEX service_image_revisions_active_tag_idx
  ON service_image_revisions(service_id, tag) WHERE status = 'active';

CREATE TABLE service_image_uploads (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  expected_length INTEGER NOT NULL CHECK (expected_length > 0),
  received_length INTEGER NOT NULL DEFAULT 0 CHECK (received_length >= 0 AND received_length <= expected_length),
  received_ranges_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(received_ranges_json) AND json_type(received_ranges_json) = 'array'),
  expected_sha256 TEXT NOT NULL,
  temporary_path TEXT NOT NULL UNIQUE,
  oidc_metadata_json TEXT NOT NULL CHECK (json_valid(oidc_metadata_json)),
  status TEXT NOT NULL CHECK (status IN ('uploading', 'importing', 'deploying', 'succeeded', 'failed', 'superseded')),
  image_revision_id TEXT REFERENCES service_image_revisions(id) ON DELETE SET NULL,
  deployment_id TEXT REFERENCES deployments(id) ON DELETE SET NULL,
  preview_id TEXT,
  preview_url TEXT,
  image_digest TEXT,
  error_code TEXT,
  error_message TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX service_image_uploads_expiry_idx ON service_image_uploads(expires_at, id);
CREATE INDEX service_image_uploads_service_tag_idx ON service_image_uploads(service_id, tag, created_at DESC);
CREATE UNIQUE INDEX service_image_uploads_active_tag_idx
  ON service_image_uploads(service_id, tag)
  WHERE status IN ('uploading', 'importing', 'deploying');

CREATE TABLE preview_deployments (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  image_revision_id TEXT REFERENCES service_image_revisions(id) ON DELETE SET NULL,
  hostname TEXT NOT NULL,
  target_port INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
  image_digest TEXT NOT NULL,
  image_reference TEXT NOT NULL,
  service_config_hash TEXT NOT NULL,
  snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)),
  status TEXT NOT NULL CHECK (status IN ('deploying', 'active', 'failed', 'stopped', 'interrupted')),
  error_code TEXT,
  error_message TEXT,
  cloudflare_records_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(cloudflare_records_json)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  finished_at INTEGER,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX preview_deployments_service_created_idx
  ON preview_deployments(service_id, created_at DESC);
CREATE INDEX preview_deployments_expiry_idx
  ON preview_deployments(expires_at, id);
CREATE UNIQUE INDEX preview_deployments_active_tag_idx
  ON preview_deployments(service_id, tag) WHERE status = 'active';
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

CREATE TABLE network_gateways (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  mode TEXT NOT NULL CHECK (mode IN ('import', 'export')),
  transport TEXT NOT NULL CHECK (transport IN ('vpc', 'mesh')),
  protocol TEXT NOT NULL CHECK (protocol IN ('tcp', 'udp')),
  interface_name TEXT,
  source_address TEXT,
  listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
  internal_slot INTEGER CHECK (internal_slot BETWEEN 192 AND 254),
  remote_host TEXT,
  remote_port INTEGER CHECK (remote_port BETWEEN 1 AND 65535),
  target_service_id TEXT REFERENCES services(id) ON DELETE RESTRICT,
  target_port INTEGER CHECK (target_port BETWEEN 1 AND 65535),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, name),
  CHECK (
    (mode = 'import' AND internal_slot IS NOT NULL AND remote_host IS NOT NULL AND remote_port IS NOT NULL AND target_service_id IS NULL AND target_port IS NULL)
    OR
    (mode = 'export' AND internal_slot IS NULL AND remote_host IS NULL AND remote_port IS NULL AND target_service_id IS NOT NULL AND target_port IS NOT NULL)
  ),
  CHECK (
    (transport = 'vpc' AND interface_name IS NOT NULL AND source_address IS NOT NULL)
    OR
    (transport = 'mesh' AND interface_name IS NULL AND source_address IS NULL)
  )
) STRICT;

CREATE UNIQUE INDEX network_gateways_internal_slot_idx
  ON network_gateways(project_id, internal_slot) WHERE mode = 'import';

CREATE UNIQUE INDEX network_gateways_export_listener_idx
  ON network_gateways(protocol, source_address, listen_port) WHERE mode = 'export';

CREATE UNIQUE INDEX network_gateways_mesh_export_listener_idx
  ON network_gateways(protocol, listen_port)
  WHERE mode = 'export' AND transport = 'mesh';

CREATE INDEX network_gateways_target_service_idx
  ON network_gateways(target_service_id) WHERE target_service_id IS NOT NULL;

CREATE TABLE object_stores (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  bucket_name TEXT NOT NULL,
  public_hostname TEXT UNIQUE,
  cors_origins_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(cors_origins_json)),
  port_forward_json TEXT CHECK (port_forward_json IS NULL OR json_valid(port_forward_json)),
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
  port_forward_json TEXT CHECK (port_forward_json IS NULL OR json_valid(port_forward_json)),
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
  port_forward_json TEXT CHECK (port_forward_json IS NULL OR json_valid(port_forward_json)),
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
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('control', 'image', 'object_store', 'postgres', 'redis', 'volume')),
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
  -- Audit history outlives deleted projects, so this scope intentionally has no foreign key.
  project_id TEXT,
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
CREATE INDEX audit_events_project_created_idx ON audit_events(project_id, created_at DESC);

CREATE TABLE analytics_trackers (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  root_domain TEXT NOT NULL CHECK (length(root_domain) BETWEEN 1 AND 253),
  mode TEXT NOT NULL DEFAULT 'opt-out' CHECK (mode IN ('cookieless', 'opt-out', 'opt-in')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, root_domain),
  UNIQUE (project_id, name)
) STRICT;

CREATE INDEX analytics_trackers_project_idx ON analytics_trackers(project_id, created_at, id);

CREATE TABLE analytics_goals (
  id TEXT PRIMARY KEY,
  tracker_id TEXT NOT NULL REFERENCES analytics_trackers(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  action_type TEXT NOT NULL CHECK (action_type IN ('path', 'event')),
  action_value TEXT NOT NULL CHECK (length(action_value) BETWEEN 1 AND 512),
  hostname TEXT CHECK (hostname IS NULL OR length(hostname) BETWEEN 1 AND 253),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX analytics_goals_tracker_idx ON analytics_goals(tracker_id, created_at, id);

CREATE TABLE analytics_funnels (
  id TEXT PRIMARY KEY,
  tracker_id TEXT NOT NULL REFERENCES analytics_trackers(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  window_value INTEGER NOT NULL CHECK (window_value BETWEEN 1 AND 10000),
  window_unit TEXT NOT NULL CHECK (window_unit IN ('minute', 'hour', 'day')),
  steps_json TEXT NOT NULL CHECK (json_valid(steps_json)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX analytics_funnels_tracker_idx ON analytics_funnels(tracker_id, created_at, id);

CREATE TABLE analytics_charts (
  id TEXT PRIMARY KEY,
  tracker_id TEXT NOT NULL REFERENCES analytics_trackers(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
  sql TEXT NOT NULL CHECK (length(sql) BETWEEN 1 AND 16384),
  visualization TEXT NOT NULL DEFAULT 'area' CHECK (visualization IN ('line', 'area', 'bar', 'value', 'table')),
  legend TEXT NOT NULL DEFAULT '' CHECK (length(legend) <= 80),
  unit TEXT CHECK (unit IS NULL OR length(unit) <= 32),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX analytics_charts_tracker_idx ON analytics_charts(tracker_id, created_at, id);

CREATE TABLE analytics_flags (
  id TEXT PRIMARY KEY,
  tracker_id TEXT NOT NULL REFERENCES analytics_trackers(id) ON DELETE CASCADE,
  key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 80),
  description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 500),
  flag_type TEXT NOT NULL CHECK (flag_type IN ('boolean', 'multivariate')),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  variants_json TEXT NOT NULL CHECK (json_valid(variants_json)),
  payload_json TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(payload_json)),
  targeting_json TEXT NOT NULL CHECK (json_valid(targeting_json)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (tracker_id, key)
) STRICT;

CREATE INDEX analytics_flags_tracker_idx ON analytics_flags(tracker_id, created_at, id);

CREATE TABLE analytics_experiments (
  id TEXT PRIMARY KEY,
  flag_id TEXT NOT NULL REFERENCES analytics_flags(id) ON DELETE CASCADE,
  control_variant TEXT NOT NULL CHECK (length(control_variant) BETWEEN 1 AND 80),
  metric_json TEXT NOT NULL CHECK (json_valid(metric_json)),
  window_value INTEGER NOT NULL CHECK (window_value BETWEEN 1 AND 10000),
  window_unit TEXT NOT NULL CHECK (window_unit IN ('minute', 'hour', 'day')),
  started_at INTEGER NOT NULL,
  ended_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX analytics_experiments_flag_idx ON analytics_experiments(flag_id, started_at, id);

PRAGMA user_version = 17;
