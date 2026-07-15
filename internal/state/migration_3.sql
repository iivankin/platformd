ALTER TABLE registry_credentials ADD COLUMN secret_encrypted BLOB;

INSERT INTO schema_migrations(version, applied_at)
VALUES (3, unixepoch('subsec') * 1000);

PRAGMA user_version = 3;
