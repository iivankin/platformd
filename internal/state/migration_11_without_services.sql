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
