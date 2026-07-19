INSERT INTO schema_migrations(version, applied_at)
VALUES (13, unixepoch('subsec') * 1000);

DROP TABLE IF EXISTS image_registry_credentials;

PRAGMA user_version = 13;
