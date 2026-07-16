UPDATE services
SET environment_json = (
  SELECT json_group_object(variable_name, variable_value)
  FROM (
    SELECT key AS variable_name, value AS variable_value
    FROM json_each(services.environment_json)
    UNION ALL
    SELECT refs.environment_name,
           '${{' || CASE refs.resource_kind
             WHEN 'service' THEN (SELECT name FROM services WHERE id = refs.resource_id)
             WHEN 'postgres' THEN (SELECT name FROM managed_postgres WHERE id = refs.resource_id)
             WHEN 'redis' THEN (SELECT name FROM managed_redis WHERE id = refs.resource_id)
             WHEN 'object_store' THEN (SELECT name FROM object_stores WHERE id = refs.resource_id)
           END || '.' || refs.output_name || '}}'
    FROM service_resource_variable_refs refs
    WHERE refs.service_id = services.id
  )
)
WHERE EXISTS (
  SELECT 1 FROM service_resource_variable_refs refs WHERE refs.service_id = services.id
);

UPDATE services SET target_port = NULL WHERE health_path IS NULL;

ALTER TABLE services RENAME COLUMN target_port TO health_port;
ALTER TABLE services RENAME COLUMN startup_timeout_seconds TO health_timeout_seconds;

DROP TABLE service_resource_variable_refs;

INSERT INTO schema_migrations(version, applied_at)
VALUES (8, unixepoch('subsec') * 1000);

PRAGMA user_version = 8;
