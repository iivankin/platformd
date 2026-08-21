package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func migrateSchemaVersionOne(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 1 to 2: %w", err)
	}
	statements := []string{
		"ALTER TABLE resource_metric_samples ADD COLUMN disk_bytes INTEGER CHECK (disk_bytes IS NULL OR disk_bytes >= 0)",
		"ALTER TABLE aggregate_metric_samples ADD COLUMN disk_bytes INTEGER CHECK (disk_bytes IS NULL OR disk_bytes >= 0)",
		"PRAGMA user_version = 2",
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 1 to 2: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 1 to 2: %w", err)
	}
	return nil
}

func migrateSchemaVersionTwo(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 2 to 3: %w", err)
	}
	statements := []string{
		`UPDATE services SET active_deployment_id = NULL, enabled = 0,
source_json = '{"type":"unconfigured"}', build_environment_json = '{}'
WHERE json_extract(source_json, '$.type') IN ('github', 'platformd_registry')`,
		`DELETE FROM preview_deployments`,
		`DELETE FROM deployments
WHERE service_id IN (SELECT id FROM services WHERE json_extract(source_json, '$.type') = 'unconfigured')`,
		// SQLite rejects DROP COLUMN for UNIQUE columns, so rebuild installation.
		`ALTER TABLE installation RENAME TO installation_drop_registry_hostname`,
		`CREATE TABLE installation (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  id TEXT NOT NULL UNIQUE,
  admin_hostname TEXT NOT NULL UNIQUE,
  access_team_domain TEXT NOT NULL,
  access_audience TEXT NOT NULL,
  console_passphrase_phc TEXT NOT NULL,
  recovery_mode INTEGER NOT NULL DEFAULT 0 CHECK (recovery_mode IN (0, 1)),
  backup_control_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT`,
		`INSERT INTO installation (
  singleton, id, admin_hostname, access_team_domain, access_audience,
  console_passphrase_phc, recovery_mode, backup_control_target_id, created_at, updated_at
)
SELECT
  singleton, id, admin_hostname, access_team_domain, access_audience,
  console_passphrase_phc, recovery_mode, backup_control_target_id, created_at, updated_at
FROM installation_drop_registry_hostname`,
		`DROP TABLE installation_drop_registry_hostname`,
		`ALTER TABLE services DROP COLUMN build_environment_json`,
		`ALTER TABLE deployments ADD COLUMN image_revision_id TEXT`,
		`DROP TABLE registry_uploads`,
		`DROP TABLE registry_tags`,
		`DROP TABLE registry_manifests`,
		`DROP TABLE registry_credentials`,
		`DROP TABLE registry_repositories`,
		`DROP TABLE github_app_settings`,
		`DROP TABLE preview_deployments`,
		`CREATE TABLE service_image_revisions (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('production', 'preview')),
  archive_path TEXT NOT NULL UNIQUE,
  archive_sha256 TEXT NOT NULL,
  image_digest TEXT,
  deployment_id TEXT REFERENCES deployments(id) ON DELETE SET NULL,
  preview_id TEXT,
  oidc_metadata_json TEXT NOT NULL CHECK (json_valid(oidc_metadata_json)),
  status TEXT NOT NULL CHECK (status IN ('importing', 'active', 'retired', 'failed')),
  created_at INTEGER NOT NULL,
  activated_at INTEGER,
  retired_at INTEGER,
  expires_at INTEGER
) STRICT`,
		`CREATE INDEX service_image_revisions_service_created_idx ON service_image_revisions(service_id, created_at DESC)`,
		`CREATE INDEX service_image_revisions_gc_idx ON service_image_revisions(status, retired_at, expires_at)`,
		`CREATE UNIQUE INDEX service_image_revisions_active_tag_idx ON service_image_revisions(service_id, tag) WHERE status = 'active'`,
		`CREATE TABLE service_image_uploads (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  expected_length INTEGER NOT NULL CHECK (expected_length > 0),
  received_length INTEGER NOT NULL DEFAULT 0 CHECK (received_length >= 0 AND received_length <= expected_length),
  expected_sha256 TEXT NOT NULL,
  temporary_path TEXT NOT NULL UNIQUE,
  oidc_metadata_json TEXT NOT NULL CHECK (json_valid(oidc_metadata_json)),
  status TEXT NOT NULL CHECK (status IN ('uploading', 'importing', 'deploying', 'succeeded', 'failed', 'superseded')),
  image_revision_id TEXT REFERENCES service_image_revisions(id) ON DELETE SET NULL,
  deployment_id TEXT REFERENCES deployments(id) ON DELETE SET NULL,
  preview_id TEXT,
  preview_url TEXT,
  image_digest TEXT,
  error_code TEXT,
  error_message TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT`,
		`CREATE INDEX service_image_uploads_expiry_idx ON service_image_uploads(expires_at, id)`,
		`CREATE INDEX service_image_uploads_service_tag_idx ON service_image_uploads(service_id, tag, created_at DESC)`,
		`CREATE UNIQUE INDEX service_image_uploads_active_tag_idx ON service_image_uploads(service_id, tag) WHERE status IN ('uploading', 'importing', 'deploying')`,
		`CREATE TABLE preview_deployments (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  image_revision_id TEXT REFERENCES service_image_revisions(id) ON DELETE SET NULL,
  hostname TEXT NOT NULL,
  target_port INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
  image_digest TEXT NOT NULL,
  image_reference TEXT NOT NULL,
  service_config_hash TEXT NOT NULL,
  snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)),
  status TEXT NOT NULL CHECK (status IN ('deploying', 'active', 'failed', 'stopped', 'interrupted')),
  error_code TEXT,
  error_message TEXT,
  cloudflare_records_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(cloudflare_records_json)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  finished_at INTEGER,
  expires_at INTEGER NOT NULL
) STRICT`,
		`CREATE INDEX preview_deployments_service_created_idx ON preview_deployments(service_id, created_at DESC)`,
		`CREATE INDEX preview_deployments_expiry_idx ON preview_deployments(expires_at, id)`,
		`CREATE UNIQUE INDEX preview_deployments_active_tag_idx ON preview_deployments(service_id, tag) WHERE status = 'active'`,
		`CREATE UNIQUE INDEX preview_deployments_active_hostname_idx ON preview_deployments(hostname) WHERE status = 'active'`,
		`DELETE FROM backups WHERE resource_kind = 'registry'`,
		`DROP INDEX backups_resource_started_idx`,
		`DROP INDEX backups_scheduled_occurrence_idx`,
		`ALTER TABLE backups RENAME TO backups_v2`,
		`CREATE TABLE backups (
  id TEXT PRIMARY KEY,
  target_id TEXT NOT NULL,
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('control', 'image', 'object_store', 'postgres', 'redis', 'volume')),
  resource_id TEXT NOT NULL,
  scheduled_occurrence INTEGER,
  generation_id TEXT,
  status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
  size_bytes INTEGER CHECK (size_bytes >= 0),
  error_code TEXT,
  error_message TEXT,
  started_at INTEGER NOT NULL,
  finished_at INTEGER
) STRICT`,
		`INSERT INTO backups SELECT * FROM backups_v2`,
		`DROP TABLE backups_v2`,
		`CREATE INDEX backups_resource_started_idx ON backups(target_id, resource_kind, resource_id, started_at DESC)`,
		`CREATE UNIQUE INDEX backups_scheduled_occurrence_idx ON backups(resource_kind, resource_id, scheduled_occurrence) WHERE scheduled_occurrence IS NOT NULL`,
		`PRAGMA user_version = 3`,
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 2 to 3: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 2 to 3: %w", err)
	}
	return nil
}

func migrateSchemaVersionThree(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 3 to 4: %w", err)
	}
	statements := []string{
		`ALTER TABLE services ADD COLUMN port_forward_json TEXT CHECK (port_forward_json IS NULL OR json_valid(port_forward_json))`,
		`PRAGMA user_version = 4`,
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 3 to 4: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 3 to 4: %w", err)
	}
	return nil
}

func migrateSchemaVersionFour(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 4 to 5: %w", err)
	}
	for _, table := range []string{"managed_postgres", "object_stores"} {
		var exists int
		if err := transaction.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&exists); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 4 to 5: %w", err), transaction.Rollback())
		}
		if exists == 0 {
			continue
		}
		statement := fmt.Sprintf(
			`ALTER TABLE %s ADD COLUMN port_forward_json TEXT CHECK (port_forward_json IS NULL OR json_valid(port_forward_json))`,
			table,
		)
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 4 to 5: %w", err), transaction.Rollback())
		}
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 5`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 4 to 5: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 4 to 5: %w", err)
	}
	return nil
}

func migrateSchemaVersionFive(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 5 to 6: %w", err)
	}
	var exists int
	if err := transaction.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'managed_redis'`,
	).Scan(&exists); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 5 to 6: %w", err), transaction.Rollback())
	}
	if exists == 1 {
		if _, err := transaction.ExecContext(ctx,
			`ALTER TABLE managed_redis ADD COLUMN port_forward_json TEXT CHECK (port_forward_json IS NULL OR json_valid(port_forward_json))`,
		); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 5 to 6: %w", err), transaction.Rollback())
		}
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 6`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 5 to 6: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 5 to 6: %w", err)
	}
	return nil
}

func migrateSchemaVersionSix(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 6 to 7: %w", err)
	}
	var exists int
	if err := transaction.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'projects'`,
	).Scan(&exists); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 6 to 7: %w", err), transaction.Rollback())
	}
	if exists == 1 {
		for _, statement := range []string{
			`ALTER TABLE projects ADD COLUMN icon_bytes BLOB CHECK (icon_bytes IS NULL OR length(icon_bytes) BETWEEN 1 AND 131072)`,
			`ALTER TABLE projects ADD COLUMN icon_content_type TEXT CHECK (
  icon_content_type IS NULL
  OR icon_content_type IN ('image/png', 'image/jpeg', 'image/webp')
)`,
		} {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return errors.Join(fmt.Errorf("migrate SQLite schema 6 to 7: %w", err), transaction.Rollback())
			}
		}
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 7`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 6 to 7: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 6 to 7: %w", err)
	}
	return nil
}

func migrateSchemaVersionSeven(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 7 to 8: %w", err)
	}
	var exists int
	if err := transaction.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'service_image_uploads'`,
	).Scan(&exists); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 7 to 8: %w", err), transaction.Rollback())
	}
	if exists == 1 {
		if _, err := transaction.ExecContext(ctx, `
ALTER TABLE service_image_uploads
ADD COLUMN received_ranges_json TEXT NOT NULL DEFAULT '[]'
  CHECK (json_valid(received_ranges_json) AND json_type(received_ranges_json) = 'array')`,
		); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 7 to 8: %w", err), transaction.Rollback())
		}
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 8`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 7 to 8: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 7 to 8: %w", err)
	}
	return nil
}

func migrateSchemaVersionEight(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 8 to 9: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `CREATE TABLE managed_stat_samples (
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('postgres', 'redis', 'object_store')),
  resource_id TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  metrics_json TEXT NOT NULL CHECK (json_valid(metrics_json)),
  PRIMARY KEY (resource_kind, resource_id, observed_at)
) WITHOUT ROWID, STRICT`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 8 to 9: %w", err), transaction.Rollback())
	}
	if _, err := transaction.ExecContext(ctx,
		`CREATE INDEX managed_stat_samples_retention_idx ON managed_stat_samples(observed_at)`,
	); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 8 to 9: %w", err), transaction.Rollback())
	}
	var metricKindColumn int
	if err := transaction.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('resource_metric_samples') WHERE name = 'resource_kind'`,
	).Scan(&metricKindColumn); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 8 to 9: %w", err), transaction.Rollback())
	}
	if metricKindColumn == 1 {
		for _, statement := range []string{
			`ALTER TABLE resource_metric_samples RENAME TO resource_metric_samples_v8`,
			`CREATE TABLE resource_metric_samples (
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('service', 'postgres', 'redis', 'network_gateway')),
  resource_id TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  duration_millis INTEGER NOT NULL CHECK (duration_millis > 0),
  cpu_duration_millis INTEGER NOT NULL DEFAULT 0 CHECK (cpu_duration_millis >= 0),
  network_duration_millis INTEGER NOT NULL DEFAULT 0 CHECK (network_duration_millis >= 0),
  proxy_duration_millis INTEGER NOT NULL DEFAULT 0 CHECK (proxy_duration_millis >= 0),
  cpu_millicores INTEGER CHECK (cpu_millicores IS NULL OR cpu_millicores >= 0),
  cpu_peak_millicores INTEGER CHECK (cpu_peak_millicores IS NULL OR cpu_peak_millicores >= 0),
  memory_bytes INTEGER NOT NULL CHECK (memory_bytes >= 0),
  memory_peak_bytes INTEGER NOT NULL CHECK (memory_peak_bytes >= memory_bytes),
  disk_bytes INTEGER CHECK (disk_bytes IS NULL OR disk_bytes >= 0),
  network_ingress_bytes_per_second INTEGER CHECK (network_ingress_bytes_per_second IS NULL OR network_ingress_bytes_per_second >= 0),
  network_ingress_peak_bytes_per_second INTEGER CHECK (network_ingress_peak_bytes_per_second IS NULL OR network_ingress_peak_bytes_per_second >= 0),
  network_egress_bytes_per_second INTEGER CHECK (network_egress_bytes_per_second IS NULL OR network_egress_bytes_per_second >= 0),
  network_egress_peak_bytes_per_second INTEGER CHECK (network_egress_peak_bytes_per_second IS NULL OR network_egress_peak_bytes_per_second >= 0),
  running INTEGER NOT NULL CHECK (running IN (0, 1)),
  proxy_metrics_json TEXT CHECK (proxy_metrics_json IS NULL OR json_valid(proxy_metrics_json)),
  CHECK (
    (cpu_duration_millis = 0 AND cpu_millicores IS NULL AND cpu_peak_millicores IS NULL) OR
    (cpu_duration_millis > 0 AND cpu_duration_millis <= duration_millis AND cpu_millicores IS NOT NULL AND cpu_peak_millicores IS NOT NULL)
  ),
  CHECK (
    (network_duration_millis = 0 AND network_ingress_bytes_per_second IS NULL AND network_ingress_peak_bytes_per_second IS NULL AND network_egress_bytes_per_second IS NULL AND network_egress_peak_bytes_per_second IS NULL) OR
    (network_duration_millis > 0 AND network_duration_millis <= duration_millis AND network_ingress_bytes_per_second IS NOT NULL AND network_ingress_peak_bytes_per_second IS NOT NULL AND network_egress_bytes_per_second IS NOT NULL AND network_egress_peak_bytes_per_second IS NOT NULL)
  ),
  CHECK (proxy_duration_millis <= duration_millis AND (proxy_duration_millis = 0 OR proxy_metrics_json IS NOT NULL)),
  PRIMARY KEY (resource_kind, resource_id, observed_at)
) WITHOUT ROWID, STRICT`,
			`INSERT INTO resource_metric_samples(
  resource_kind, resource_id, observed_at, duration_millis,
  cpu_duration_millis, network_duration_millis, proxy_duration_millis,
  cpu_millicores, cpu_peak_millicores, memory_bytes, memory_peak_bytes, disk_bytes,
  network_ingress_bytes_per_second, network_ingress_peak_bytes_per_second,
  network_egress_bytes_per_second, network_egress_peak_bytes_per_second,
  running, proxy_metrics_json
)
SELECT
  resource_kind, resource_id, observed_at, duration_millis,
  cpu_duration_millis, network_duration_millis, proxy_duration_millis,
  cpu_millicores, cpu_peak_millicores, memory_bytes, memory_peak_bytes, disk_bytes,
  network_ingress_bytes_per_second, network_ingress_peak_bytes_per_second,
  network_egress_bytes_per_second, network_egress_peak_bytes_per_second,
  running, proxy_metrics_json
FROM resource_metric_samples_v8`,
			`DROP TABLE resource_metric_samples_v8`,
			`CREATE INDEX resource_metric_samples_retention_idx ON resource_metric_samples(observed_at)`,
		} {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return errors.Join(fmt.Errorf("migrate SQLite schema 8 to 9: %w", err), transaction.Rollback())
			}
		}
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 9`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 8 to 9: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 8 to 9: %w", err)
	}
	return nil
}

func migrateSchemaVersionNine(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 9 to 10: %w", err)
	}
	for _, statement := range []string{
		`ALTER TABLE services ADD COLUMN sentry_public_hostname TEXT`,
		`CREATE UNIQUE INDEX services_sentry_public_hostname_idx ON services(sentry_public_hostname) WHERE sentry_public_hostname IS NOT NULL`,
		`DROP TABLE resource_metric_samples`,
		`DROP TABLE aggregate_metric_samples`,
		`DROP TABLE managed_stat_samples`,
		`PRAGMA user_version = 10`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 9 to 10: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 9 to 10: %w", err)
	}
	return nil
}

func migrateSchemaVersionTen(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 10 to 11: %w", err)
	}
	for _, statement := range []string{
		`CREATE TABLE service_telemetry_credentials (
  service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  artifact_token_sha256 BLOB NOT NULL CHECK (length(artifact_token_sha256) = 32),
  updated_at INTEGER NOT NULL
) STRICT`,
		`CREATE TABLE service_telemetry_webhooks (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  event_types_json TEXT NOT NULL CHECK (json_valid(event_types_json) AND json_type(event_types_json) = 'array'),
  secret_encrypted BLOB NOT NULL,
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT`,
		`CREATE INDEX service_telemetry_webhooks_service_idx ON service_telemetry_webhooks(service_id, created_at, id)`,
		`PRAGMA user_version = 11`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 10 to 11: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 10 to 11: %w", err)
	}
	return nil
}

func migrateSchemaVersionEleven(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 11 to 12: %w", err)
	}
	for _, statement := range []string{
		`CREATE TABLE service_metric_charts (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
  metric_name TEXT NOT NULL CHECK (length(metric_name) BETWEEN 1 AND 256),
  aggregation TEXT NOT NULL CHECK (aggregation IN ('avg', 'sum', 'min', 'max')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT`,
		`CREATE INDEX service_metric_charts_service_idx ON service_metric_charts(service_id, created_at, id)`,
		`PRAGMA user_version = 12`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 11 to 12: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 11 to 12: %w", err)
	}
	return nil
}

func migrateSchemaVersionTwelve(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 12 to 13: %w", err)
	}
	for _, statement := range []string{
		`ALTER TABLE service_metric_charts RENAME TO service_metric_charts_v12`,
		`CREATE TABLE service_metric_charts (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
  metric_name TEXT NOT NULL CHECK (length(metric_name) BETWEEN 1 AND 256),
  operation TEXT NOT NULL CHECK (operation IN ('average', 'count', 'increase', 'last', 'maximum', 'minimum', 'p50', 'p90', 'p95', 'p99', 'rate', 'sum')),
  filters_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(filters_json) AND json_type(filters_json) = 'array' AND json_array_length(filters_json) <= 5),
  group_by TEXT CHECK (group_by IS NULL OR length(group_by) BETWEEN 1 AND 128),
  series_limit INTEGER NOT NULL DEFAULT 5 CHECK (series_limit BETWEEN 1 AND 20),
  visualization TEXT NOT NULL DEFAULT 'area' CHECK (visualization IN ('line', 'area', 'bar', 'value')),
  legend TEXT NOT NULL DEFAULT '' CHECK (length(legend) <= 80),
  unit TEXT CHECK (unit IS NULL OR length(unit) <= 32),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT`,
		`INSERT INTO service_metric_charts(
  id, service_id, title, metric_name, operation, filters_json, group_by,
  series_limit, visualization, legend, unit, created_at, updated_at
)
SELECT id, service_id, title, metric_name,
  CASE aggregation WHEN 'avg' THEN 'average' WHEN 'min' THEN 'minimum' WHEN 'max' THEN 'maximum' ELSE 'sum' END,
  '[]', NULL, 5, 'area', '', NULL, created_at, updated_at
FROM service_metric_charts_v12`,
		`DROP TABLE service_metric_charts_v12`,
		`CREATE INDEX service_metric_charts_service_idx ON service_metric_charts(service_id, created_at, id)`,
		`PRAGMA user_version = 13`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 12 to 13: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 12 to 13: %w", err)
	}
	return nil
}

func migrateSchemaVersionThirteen(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 13 to 14: %w", err)
	}
	for _, statement := range []string{
		`DROP TABLE service_metric_charts`,
		`CREATE TABLE service_metric_charts (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
  sql TEXT NOT NULL CHECK (length(sql) BETWEEN 1 AND 16384),
  visualization TEXT NOT NULL DEFAULT 'area' CHECK (visualization IN ('line', 'area', 'bar', 'value')),
  legend TEXT NOT NULL DEFAULT '' CHECK (length(legend) <= 80),
  unit TEXT CHECK (unit IS NULL OR length(unit) <= 32),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT`,
		`CREATE INDEX service_metric_charts_service_idx ON service_metric_charts(service_id, created_at, id)`,
		`PRAGMA user_version = 14`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 13 to 14: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 13 to 14: %w", err)
	}
	return nil
}

func migrateSchemaVersionFourteen(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 14 to 15: %w", err)
	}
	for _, statement := range []string{
		`ALTER TABLE service_metric_charts RENAME TO service_metric_charts_v14`,
		`CREATE TABLE metric_charts (
  id TEXT PRIMARY KEY,
  scope_kind TEXT NOT NULL CHECK (scope_kind IN ('installation', 'project', 'service')),
  project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
  service_id TEXT REFERENCES services(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
  sql TEXT NOT NULL CHECK (length(sql) BETWEEN 1 AND 16384),
  visualization TEXT NOT NULL DEFAULT 'area' CHECK (visualization IN ('line', 'area', 'bar', 'value')),
  legend TEXT NOT NULL DEFAULT '' CHECK (length(legend) <= 80),
  unit TEXT CHECK (unit IS NULL OR length(unit) <= 32),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  CHECK (
    (scope_kind = 'installation' AND project_id IS NULL AND service_id IS NULL) OR
    (scope_kind = 'project' AND project_id IS NOT NULL AND service_id IS NULL) OR
    (scope_kind = 'service' AND project_id IS NULL AND service_id IS NOT NULL)
  )
) STRICT`,
		`INSERT INTO metric_charts(
  id, scope_kind, project_id, service_id, title, sql, visualization, legend, unit, created_at, updated_at
)
SELECT id, 'service', NULL, service_id, title, sql, visualization, legend, unit, created_at, updated_at
FROM service_metric_charts_v14`,
		`DROP TABLE service_metric_charts_v14`,
		`CREATE INDEX metric_charts_scope_idx ON metric_charts(scope_kind, project_id, service_id, created_at, id)`,
		`PRAGMA user_version = 15`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 14 to 15: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 14 to 15: %w", err)
	}
	return nil
}

func migrateSchemaVersionFifteen(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 15 to 16: %w", err)
	}
	for _, statement := range []string{
		`ALTER TABLE services ADD COLUMN sentry_tunnel_path TEXT CHECK (
  sentry_tunnel_path IS NULL OR length(sentry_tunnel_path) BETWEEN 2 AND 256
)`,
		`PRAGMA user_version = 16`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 15 to 16: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 15 to 16: %w", err)
	}
	return nil
}

const analyticsCatalogSchema = `
CREATE TABLE analytics_trackers (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  root_domain TEXT NOT NULL CHECK (length(root_domain) BETWEEN 1 AND 253),
  mode TEXT NOT NULL DEFAULT 'opt-out' CHECK (mode IN ('cookieless', 'opt-out', 'opt-in')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, root_domain),
  UNIQUE (project_id, name)
) STRICT;
CREATE INDEX analytics_trackers_project_idx ON analytics_trackers(project_id, created_at, id);
CREATE TABLE analytics_goals (
  id TEXT PRIMARY KEY,
  tracker_id TEXT NOT NULL REFERENCES analytics_trackers(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  action_type TEXT NOT NULL CHECK (action_type IN ('path', 'event')),
  action_value TEXT NOT NULL CHECK (length(action_value) BETWEEN 1 AND 512),
  hostname TEXT CHECK (hostname IS NULL OR length(hostname) BETWEEN 1 AND 253),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX analytics_goals_tracker_idx ON analytics_goals(tracker_id, created_at, id);
CREATE TABLE analytics_funnels (
  id TEXT PRIMARY KEY,
  tracker_id TEXT NOT NULL REFERENCES analytics_trackers(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  window_value INTEGER NOT NULL CHECK (window_value BETWEEN 1 AND 10000),
  window_unit TEXT NOT NULL CHECK (window_unit IN ('minute', 'hour', 'day')),
  steps_json TEXT NOT NULL CHECK (json_valid(steps_json)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX analytics_funnels_tracker_idx ON analytics_funnels(tracker_id, created_at, id);
CREATE TABLE analytics_charts (
  id TEXT PRIMARY KEY,
  tracker_id TEXT NOT NULL REFERENCES analytics_trackers(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
  sql TEXT NOT NULL CHECK (length(sql) BETWEEN 1 AND 16384),
  visualization TEXT NOT NULL DEFAULT 'area' CHECK (visualization IN ('line', 'area', 'bar', 'value', 'table')),
  legend TEXT NOT NULL DEFAULT '' CHECK (length(legend) <= 80),
  unit TEXT CHECK (unit IS NULL OR length(unit) <= 32),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX analytics_charts_tracker_idx ON analytics_charts(tracker_id, created_at, id);
CREATE TABLE analytics_flags (
  id TEXT PRIMARY KEY,
  tracker_id TEXT NOT NULL REFERENCES analytics_trackers(id) ON DELETE CASCADE,
  key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 80),
  description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 500),
  flag_type TEXT NOT NULL CHECK (flag_type IN ('boolean', 'multivariate')),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  variants_json TEXT NOT NULL CHECK (json_valid(variants_json)),
  payload_json TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(payload_json)),
  targeting_json TEXT NOT NULL CHECK (json_valid(targeting_json)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (tracker_id, key)
) STRICT;
CREATE INDEX analytics_flags_tracker_idx ON analytics_flags(tracker_id, created_at, id);
CREATE TABLE analytics_experiments (
  id TEXT PRIMARY KEY,
  flag_id TEXT NOT NULL REFERENCES analytics_flags(id) ON DELETE CASCADE,
  control_variant TEXT NOT NULL CHECK (length(control_variant) BETWEEN 1 AND 80),
  metric_json TEXT NOT NULL CHECK (json_valid(metric_json)),
  window_value INTEGER NOT NULL CHECK (window_value BETWEEN 1 AND 10000),
  window_unit TEXT NOT NULL CHECK (window_unit IN ('minute', 'hour', 'day')),
  started_at INTEGER NOT NULL,
  ended_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX analytics_experiments_flag_idx ON analytics_experiments(flag_id, started_at, id);
`

func migrateSchemaVersionSixteen(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 16 to 17: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, analyticsCatalogSchema); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 16 to 17: %w", err), transaction.Rollback())
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 17`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 16 to 17: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 16 to 17: %w", err)
	}
	return nil
}

const mailSchema = `
CREATE TABLE smtp_settings (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  host TEXT NOT NULL CHECK (length(host) BETWEEN 1 AND 253),
  port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
  username TEXT NOT NULL CHECK (length(username) <= 320),
  password_encrypted BLOB NOT NULL,
  from_address TEXT NOT NULL CHECK (length(from_address) BETWEEN 3 AND 320),
  from_name TEXT NOT NULL DEFAULT '' CHECK (length(from_name) <= 80),
  encryption TEXT NOT NULL CHECK (encryption IN ('none', 'starttls', 'tls')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE TABLE mail_error_alerts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  recipients_json TEXT NOT NULL CHECK (
    json_valid(recipients_json) AND json_type(recipients_json) = 'array'
    AND json_array_length(recipients_json) BETWEEN 1 AND 20
  ),
  event_types_json TEXT NOT NULL CHECK (
    json_valid(event_types_json) AND json_type(event_types_json) = 'array'
    AND json_array_length(event_types_json) BETWEEN 1 AND 3
  ),
  service_ids_json TEXT NOT NULL CHECK (
    json_valid(service_ids_json) AND json_type(service_ids_json) = 'array'
    AND json_array_length(service_ids_json) <= 50
  ),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX mail_error_alerts_created_idx ON mail_error_alerts(created_at, id);
CREATE TABLE mail_metric_alerts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  recipients_json TEXT NOT NULL CHECK (
    json_valid(recipients_json) AND json_type(recipients_json) = 'array'
    AND json_array_length(recipients_json) BETWEEN 1 AND 20
  ),
  scope_kind TEXT NOT NULL CHECK (scope_kind IN ('installation', 'project', 'service')),
  project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
  service_id TEXT REFERENCES services(id) ON DELETE CASCADE,
  sql TEXT NOT NULL CHECK (length(sql) BETWEEN 1 AND 16384),
  operator TEXT NOT NULL CHECK (operator IN ('gt', 'gte', 'lt', 'lte')),
  threshold REAL NOT NULL,
  window_seconds INTEGER NOT NULL CHECK (window_seconds BETWEEN 60 AND 86400),
  firing INTEGER NOT NULL DEFAULT 0 CHECK (firing IN (0, 1)),
  last_value REAL,
  last_evaluated_at INTEGER,
  last_sent_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  CHECK (
    (scope_kind = 'installation' AND project_id IS NULL AND service_id IS NULL) OR
    (scope_kind = 'project' AND project_id IS NOT NULL AND service_id IS NULL) OR
    (scope_kind = 'service' AND project_id IS NOT NULL AND service_id IS NOT NULL)
  )
) STRICT;
CREATE INDEX mail_metric_alerts_scope_idx ON mail_metric_alerts(scope_kind, project_id, service_id, created_at, id);
`

func migrateSchemaVersionSeventeen(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 17 to 18: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, mailSchema); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 17 to 18: %w", err), transaction.Rollback())
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 18`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 17 to 18: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 17 to 18: %w", err)
	}
	return nil
}

const hostCatalogSchema = `
CREATE TABLE hosts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  public_ipv4 TEXT CHECK (public_ipv4 IS NULL OR length(public_ipv4) BETWEEN 7 AND 15),
  token_hmac BLOB NOT NULL CHECK (length(token_hmac) = 32),
  last_seen_at INTEGER,
  joined_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE TABLE host_join_tokens (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  token_hmac BLOB NOT NULL CHECK (length(token_hmac) = 32),
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER,
  created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX host_join_tokens_created_idx ON host_join_tokens(created_at, id);
`

func migrateSchemaVersionEighteen(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 18 to 19: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, hostCatalogSchema); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 18 to 19: %w", err), transaction.Rollback())
	}
	if _, err := transaction.ExecContext(ctx, `ALTER TABLE services ADD COLUMN host_id TEXT REFERENCES hosts(id) ON DELETE RESTRICT`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 18 to 19: %w", err), transaction.Rollback())
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 19`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 18 to 19: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 18 to 19: %w", err)
	}
	return nil
}

func migrateSchemaVersionNineteen(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 19 to 20: %w", err)
	}
	const listenersTable = `CREATE TABLE service_listeners (
  protocol TEXT NOT NULL CHECK (protocol IN ('tcp', 'udp')),
  public_port INTEGER NOT NULL CHECK (public_port BETWEEN 1 AND 65535),
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  target_port INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (service_id, protocol, public_port)
) WITHOUT ROWID, STRICT`
	var exists int
	if err := transaction.QueryRowContext(ctx, `
SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'service_listeners'`).Scan(&exists); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 19 to 20: %w", err), transaction.Rollback())
	}
	statements := []string{listenersTable, `PRAGMA user_version = 20`}
	if exists == 1 {
		statements = []string{
			`ALTER TABLE service_listeners RENAME TO service_listeners_global_ports`,
			listenersTable,
			`INSERT INTO service_listeners(protocol, public_port, service_id, target_port, created_at)
SELECT protocol, public_port, service_id, target_port, created_at
FROM service_listeners_global_ports`,
			`DROP TABLE service_listeners_global_ports`,
			`PRAGMA user_version = 20`,
		}
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 19 to 20: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 19 to 20: %w", err)
	}
	return nil
}

func migrateSchemaVersionTwenty(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 20 to 21: %w", err)
	}
	statements := []string{
		`ALTER TABLE services ADD COLUMN otlp_trace_public_hostname TEXT`,
		`ALTER TABLE services ADD COLUMN otlp_trace_path TEXT CHECK (
  otlp_trace_path IS NULL OR length(otlp_trace_path) BETWEEN 2 AND 256
)`,
		`CREATE UNIQUE INDEX services_otlp_trace_public_hostname_idx
ON services(otlp_trace_public_hostname) WHERE otlp_trace_public_hostname IS NOT NULL`,
		`PRAGMA user_version = 21`,
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 20 to 21: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 20 to 21: %w", err)
	}
	return nil
}

func migrateSchemaVersionTwentyOne(ctx context.Context, database *sql.DB) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration 21 to 22: %w", err)
	}
	for _, statement := range []string{
		`ALTER TABLE analytics_trackers DROP COLUMN mode`,
		`PRAGMA user_version = 22`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 21 to 22: %w", err), transaction.Rollback())
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 21 to 22: %w", err)
	}
	return nil
}
