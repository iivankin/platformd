CREATE TABLE managed_postgres_extensions (
  postgres_id TEXT NOT NULL REFERENCES managed_postgres(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  version TEXT NOT NULL,
  recipe_digest TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (postgres_id, name)
) WITHOUT ROWID, STRICT;

INSERT INTO schema_migrations(version, applied_at)
VALUES (9, unixepoch('subsec') * 1000);

PRAGMA user_version = 9;
