CREATE TABLE network_gateways (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  mode TEXT NOT NULL CHECK (mode IN ('import', 'export')),
  transport TEXT NOT NULL CHECK (transport IN ('vpc', 'mesh')),
  protocol TEXT NOT NULL CHECK (protocol IN ('tcp', 'udp')),
  interface_name TEXT NOT NULL,
  source_address TEXT NOT NULL,
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
  )
) STRICT;

CREATE UNIQUE INDEX network_gateways_internal_slot_idx
  ON network_gateways(project_id, internal_slot) WHERE mode = 'import';

CREATE UNIQUE INDEX network_gateways_export_listener_idx
  ON network_gateways(protocol, source_address, listen_port) WHERE mode = 'export';

CREATE INDEX network_gateways_target_service_idx
  ON network_gateways(target_service_id) WHERE target_service_id IS NOT NULL;

INSERT INTO schema_migrations(version, applied_at)
VALUES (14, unixepoch('subsec') * 1000);

PRAGMA user_version = 14;
