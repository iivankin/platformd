CREATE TABLE services_v11 (
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
  FOREIGN KEY (active_deployment_id) REFERENCES deployments_v11(id) DEFERRABLE INITIALLY DEFERRED,
  UNIQUE (project_id, name)
) STRICT;

CREATE TABLE deployments_v11 (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services_v11(id) ON DELETE CASCADE,
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

INSERT INTO services_v11(
  id, project_id, name, source_json, command_json, args_json, environment_json,
  health_port, health_path, health_timeout_seconds, cpu_millis, memory_bytes,
  enabled, active_deployment_id, created_at, updated_at
)
SELECT
  s.id, s.project_id, s.name,
  json_object(
    'type', CASE
      WHEN s.image_credential_id IS NOT NULL THEN 'private_image'
      WHEN EXISTS (
        SELECT 1 FROM installation i
        WHERE i.singleton = 1 AND i.registry_hostname IS NOT NULL
          AND instr(s.image_reference, i.registry_hostname || '/') = 1
      ) THEN 'platformd_registry'
      ELSE 'public_image'
    END,
    'autoUpdate', json('true'),
    'image', json_object(
      'reference', s.image_reference,
      'credentialId', COALESCE(s.image_credential_id, '')
    )
  ),
  s.command_json, s.args_json, s.environment_json,
  s.health_port, s.health_path, s.health_timeout_seconds, s.cpu_millis,
  s.memory_bytes, s.enabled, s.active_deployment_id, s.created_at, s.updated_at
FROM services s;

INSERT INTO deployments_v11(
  id, service_id, image_digest, image_reference, source_revision,
  source_commit_message, service_config_hash, snapshot_json, status,
  error_code, error_message, created_at, finished_at
)
SELECT
  d.id, d.service_id, d.image_digest,
  COALESCE(json_extract(d.snapshot_json, '$.imageReference'), ''), NULL, NULL,
  d.service_config_hash,
  json_remove(
    json_set(
      d.snapshot_json,
      '$.source', json_object(
        'type', CASE
          WHEN COALESCE(json_extract(d.snapshot_json, '$.imageCredentialId'), '') != '' THEN 'private_image'
          WHEN EXISTS (
            SELECT 1 FROM installation i
            WHERE i.singleton = 1 AND i.registry_hostname IS NOT NULL
              AND instr(json_extract(d.snapshot_json, '$.imageReference'), i.registry_hostname || '/') = 1
          ) THEN 'platformd_registry'
          ELSE 'public_image'
        END,
        'autoUpdate', json('true'),
        'image', json_object(
          'reference', json_extract(d.snapshot_json, '$.imageReference'),
          'credentialId', COALESCE(json_extract(d.snapshot_json, '$.imageCredentialId'), '')
        )
      )
    ),
    '$.imageReference', '$.imageCredentialId'
  ),
  d.status, d.error_code, d.error_message, d.created_at, d.finished_at
FROM deployments d;

DROP TABLE deployments;
DROP TABLE services;
ALTER TABLE services_v11 RENAME TO services;
ALTER TABLE deployments_v11 RENAME TO deployments;

CREATE INDEX deployments_service_created_idx ON deployments(service_id, created_at DESC);
CREATE INDEX deployments_retry_pair_idx ON deployments(service_id, service_config_hash, image_digest, created_at DESC);

CREATE TABLE github_app_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  app_id INTEGER NOT NULL CHECK (app_id > 0),
  app_slug TEXT NOT NULL,
  private_key_encrypted BLOB NOT NULL,
  webhook_secret_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO schema_migrations(version, applied_at)
VALUES (11, unixepoch('subsec') * 1000);

PRAGMA user_version = 11;
