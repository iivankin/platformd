CREATE TABLE cloudflare_dns_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  api_token_encrypted BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO schema_migrations(version, applied_at)
VALUES (12, unixepoch('subsec') * 1000);

PRAGMA user_version = 12;
