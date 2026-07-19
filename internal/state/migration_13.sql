CREATE TABLE service_image_credentials (
  service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  registry_host TEXT NOT NULL,
  username TEXT NOT NULL,
  password_encrypted BLOB NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

DROP TABLE IF EXISTS image_registry_credentials;

INSERT INTO schema_migrations(version, applied_at)
VALUES (13, unixepoch('subsec') * 1000);

PRAGMA user_version = 13;
