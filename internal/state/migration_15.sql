CREATE TABLE cloudflare_mesh_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  account_id TEXT NOT NULL,
  api_token_encrypted BLOB NOT NULL,
  node_id TEXT NOT NULL,
  node_name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE network_gateways_v15 (
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

INSERT INTO network_gateways_v15(
  id, project_id, name, mode, transport, protocol, interface_name, source_address,
  listen_port, internal_slot, remote_host, remote_port, target_service_id,
  target_port, created_at, updated_at
)
SELECT
  id, project_id, name, mode, transport, protocol,
  CASE WHEN transport = 'vpc' THEN interface_name END,
  CASE WHEN transport = 'vpc' THEN source_address END,
  listen_port, internal_slot, remote_host, remote_port, target_service_id,
  target_port, created_at, updated_at
FROM network_gateways;

DROP TABLE network_gateways;
ALTER TABLE network_gateways_v15 RENAME TO network_gateways;

CREATE UNIQUE INDEX network_gateways_internal_slot_idx
  ON network_gateways(project_id, internal_slot) WHERE mode = 'import';
CREATE UNIQUE INDEX network_gateways_export_listener_idx
  ON network_gateways(protocol, source_address, listen_port) WHERE mode = 'export';
CREATE UNIQUE INDEX network_gateways_mesh_export_listener_idx
  ON network_gateways(protocol, listen_port)
  WHERE mode = 'export' AND transport = 'mesh';
CREATE INDEX network_gateways_target_service_idx
  ON network_gateways(target_service_id) WHERE target_service_id IS NOT NULL;

INSERT INTO schema_migrations(version, applied_at)
VALUES (15, unixepoch('subsec') * 1000);

PRAGMA user_version = 15;
