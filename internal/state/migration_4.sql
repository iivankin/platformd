CREATE TABLE resource_metric_samples (
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('service', 'postgres', 'redis')),
  resource_id TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  cpu_usage_micros INTEGER NOT NULL CHECK (cpu_usage_micros >= 0),
  memory_bytes INTEGER NOT NULL CHECK (memory_bytes >= 0),
  network_rx_bytes INTEGER CHECK (network_rx_bytes IS NULL OR network_rx_bytes >= 0),
  network_tx_bytes INTEGER CHECK (network_tx_bytes IS NULL OR network_tx_bytes >= 0),
  running INTEGER NOT NULL CHECK (running IN (0, 1)),
  PRIMARY KEY (resource_kind, resource_id, observed_at)
) WITHOUT ROWID, STRICT;

CREATE INDEX resource_metric_samples_retention_idx
  ON resource_metric_samples(observed_at);

INSERT INTO schema_migrations(version, applied_at)
VALUES (4, unixepoch('subsec') * 1000);

PRAGMA user_version = 4;
