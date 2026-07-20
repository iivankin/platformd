CREATE TABLE cloudflare_mesh_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  account_id TEXT NOT NULL,
  api_token_encrypted BLOB NOT NULL,
  node_id TEXT NOT NULL,
  node_name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO schema_migrations(version, applied_at)
VALUES (15, unixepoch('subsec') * 1000);

PRAGMA user_version = 15;
