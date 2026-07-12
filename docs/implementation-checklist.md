# Implementation and release checklist

This checklist maps the implementation to the normative specification. A checked item requires direct evidence from code plus the test or runtime artifact named beside it; existence of a handler or passing unrelated tests is not sufficient.

## Distribution, bootstrap, and state

- [ ] Self-contained amd64 release embeds UI and verified runtime bundle. Evidence: release manifest test and clean Debian install.
- [ ] `platformd init` is the only public command and is interruption-safe. Evidence: bootstrap integration matrix.
- [ ] Master key lifecycle, one-time recovery prompt, passphrase verifier, Origin certificates, and Access configuration match the spec. Evidence: bootstrap and secret-at-rest tests.
- [ ] SQLite schema, migrations, writer serialization, audit, observational operations, and startup interrupted-state repair are complete. Evidence: database and crash tests.
- [ ] Filesystem ownership, atomic payload publication, orphan cleanup, and single-filesystem checks are complete. Evidence: fault-injection tests.

## Runtime and networking

- [ ] Private libpod/containers-storage adapter uses only bundled helpers/config and absolute paths. Evidence: clean-host contract test.
- [ ] Startup purges container records/writable layers while retaining only compatible image cache. Evidence: privileged restart tests.
- [ ] Delegated cgroup topology, limits, shutdown, exec, PTY, logs, and health checks match the spec. Evidence: cgroup/process integration suite.
- [ ] Per-project Netavark networks, built-in DNS, nftables isolation, and internal service/resource names work. Evidence: packet-level isolation suite.

## Control plane and deployment

- [ ] Cloudflare Access JWT validation, JWKS bounds, CSRF, WebSocket auth, certificates, and hostname routing are complete. Evidence: auth/proxy integration suite.
- [ ] Services, immutable Deployments, watcher polling, stop-first apply, rollback, secrets, credentials, and Volumes match the spec. Evidence: deployment/crash suite.
- [ ] REST/OpenAPI and stateless MCP 2025-11-25 expose the same authorization boundaries and operations. Evidence: protocol conformance suite.
- [ ] Ghostty-Web container PTY, passphrase-protected Ghostty-Web root PTY, and token-authorized bounded root exec are complete. Evidence: terminal/exec browser E2E tests.

## Managed data services

- [ ] OCI Registry protocol subset, Basic auth, browsing/deletion, cleanup, backup, retention, and restore are complete. Evidence: Distribution client/conformance tests.
- [ ] Private S3 subset, SigV4/presign, encrypted chunks/multipart, browser, cleanup, backup, retention, and restore are complete. Evidence: AWS SDK compatibility suite.
- [ ] Managed PostgreSQL official-image profile, non-superuser owner, SQL/data browser, backup/restore, and version transfer are complete. Evidence: multi-version engine suite.
- [ ] Managed Redis official-image profile, RDB persistence, browser, stable-FD backup/restore, and version transfer are complete. Evidence: multi-version engine suite.
- [ ] Remote backup target probing, independent UTC schedules, encrypted generations, control snapshot, and full restore are complete. Evidence: backup and fresh-VPS recovery suites.

## Operations and release

- [ ] Disk pressure, cleanup ordering, reserve file, freeze/unfreeze, metrics shown in UI, and 7-day audit/log retention are complete. Evidence: disk/inode pressure suite.
- [ ] Signed idle-only self-update, clean runtime recreation, pre-commit rollback, post-commit forward fix, and cache fallback are complete. Evidence: update fault-injection matrix.
- [ ] All admin UI sections and data/console surfaces are implemented without nested cards and pass browser E2E/accessibility checks.
- [ ] Full release acceptance scenario in §32 passes on the Debian 13 server with Cloudflare domains.
- [ ] Completion audit links every specification requirement to current evidence and contains no unverified checked items.
