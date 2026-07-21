INSERT INTO schema_migrations(version, applied_at)
VALUES (16, unixepoch('subsec') * 1000);

PRAGMA user_version = 16;
