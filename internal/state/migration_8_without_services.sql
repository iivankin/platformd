INSERT INTO schema_migrations(version, applied_at)
VALUES (8, unixepoch('subsec') * 1000);

PRAGMA user_version = 8;
