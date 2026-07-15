CREATE TABLE service_resource_variable_refs (
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  environment_name TEXT NOT NULL,
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('service', 'postgres', 'redis', 'object_store')),
  resource_id TEXT NOT NULL,
  output_name TEXT NOT NULL,
  PRIMARY KEY (service_id, environment_name)
) WITHOUT ROWID, STRICT;

CREATE INDEX service_resource_variable_refs_target_idx
  ON service_resource_variable_refs(resource_kind, resource_id);

INSERT INTO schema_migrations(version, applied_at)
VALUES (5, unixepoch('subsec') * 1000);

PRAGMA user_version = 5;
