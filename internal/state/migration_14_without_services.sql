INSERT INTO schema_migrations(version, applied_at)
VALUES (14, unixepoch('subsec') * 1000);

PRAGMA user_version = 14;
