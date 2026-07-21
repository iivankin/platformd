CREATE TABLE volume_initializations (
  volume_id TEXT PRIMARY KEY REFERENCES volumes(id) ON DELETE CASCADE,
  initialized_at INTEGER NOT NULL
) STRICT;

INSERT INTO schema_migrations(version, applied_at)
VALUES (16, unixepoch('subsec') * 1000);

PRAGMA user_version = 16;
