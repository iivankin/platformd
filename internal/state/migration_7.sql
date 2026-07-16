ALTER TABLE service_domains RENAME TO service_domains_v6;

CREATE TABLE service_domains (
  hostname TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  target_port INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
  created_at INTEGER NOT NULL
) WITHOUT ROWID, STRICT;

INSERT INTO service_domains(hostname, service_id, target_port, created_at)
SELECT d.hostname, d.service_id, s.target_port, d.created_at
FROM service_domains_v6 d
JOIN services s ON s.id = d.service_id
WHERE s.target_port IS NOT NULL;

DROP TABLE service_domains_v6;

CREATE TABLE service_listeners (
  protocol TEXT NOT NULL CHECK (protocol IN ('tcp', 'udp')),
  public_port INTEGER NOT NULL CHECK (public_port BETWEEN 1 AND 65535),
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  target_port INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (protocol, public_port)
) WITHOUT ROWID, STRICT;

CREATE INDEX service_listeners_service_idx
  ON service_listeners(service_id, protocol, public_port);

INSERT INTO schema_migrations(version, applied_at)
VALUES (7, unixepoch('subsec') * 1000);

PRAGMA user_version = 7;
