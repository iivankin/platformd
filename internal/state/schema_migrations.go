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
	if _, err := transaction.ExecContext(ctx, `CREATE TABLE error_trackers (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  volume_id TEXT NOT NULL UNIQUE,
  public_hostname TEXT UNIQUE,
  backup_enabled INTEGER NOT NULL DEFAULT 0 CHECK (backup_enabled IN (0, 1)),
  backup_cron TEXT,
  backup_retention_count INTEGER NOT NULL DEFAULT 7 CHECK (backup_retention_count BETWEEN 1 AND 100),
  backup_target_id TEXT REFERENCES backup_targets(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, name)
) STRICT`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 9 to 10: %w", err), transaction.Rollback())
	}
	var backupsExist int
	if err := transaction.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'backups'`,
	).Scan(&backupsExist); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 9 to 10: %w", err), transaction.Rollback())
	}
	statements := []string{}
	if backupsExist == 1 {
		statements = append(statements,
			`DROP INDEX IF EXISTS backups_resource_started_idx`,
			`DROP INDEX IF EXISTS backups_scheduled_occurrence_idx`,
			`ALTER TABLE backups RENAME TO backups_v9`,
			`CREATE TABLE backups (
  id TEXT PRIMARY KEY,
  target_id TEXT NOT NULL,
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('control', 'error_tracker', 'image', 'object_store', 'postgres', 'redis', 'volume')),
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
			`INSERT INTO backups SELECT * FROM backups_v9`,
			`DROP TABLE backups_v9`,
			`CREATE INDEX backups_resource_started_idx ON backups(target_id, resource_kind, resource_id, started_at DESC)`,
			`CREATE UNIQUE INDEX backups_scheduled_occurrence_idx ON backups(resource_kind, resource_id, scheduled_occurrence) WHERE scheduled_occurrence IS NOT NULL`)
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return errors.Join(fmt.Errorf("migrate SQLite schema 9 to 10: %w", err), transaction.Rollback())
		}
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 10`); err != nil {
		return errors.Join(fmt.Errorf("migrate SQLite schema 9 to 10: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration 9 to 10: %w", err)
	}
	return nil
}
