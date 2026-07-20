# platformd

`platformd` is a self-hosted application platform for one VPS. It deploys services, PostgreSQL, Redis, object storage, domains, backups, logs, metrics, terminals, and private networking from one project canvas.

The server, admin UI, and container runtime are distributed as one `platformd` release executable. `platformd-forward` is a small local helper that connects short-lived API/MCP port-forward tickets to a localhost TCP port.

## Requirements

- A dedicated `amd64` VPS running Debian 13 or Ubuntu 24.04
- Root access, systemd 255+, cgroup v2, OverlayFS, nftables, and a free TCP port 443
- A Cloudflare-proxied admin hostname, Cloudflare Access application, and matching Origin certificate

## Install

Install the server on the VPS, then start its interactive bootstrap:

```bash
curl -fsSL https://raw.githubusercontent.com/iivankin/platformd/main/install.sh | sudo sh -s -- platformd
sudo platformd init
```

`init` validates the host and asks for the admin hostname, Cloudflare Access settings, Origin certificate, and console passphrase. It also prints the master recovery key once; save it outside the VPS. Further configuration happens in the admin UI.

Install the local port-forward helper on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/iivankin/platformd/main/install.sh | sh -s -- forward
```

Both modes download the matching asset and verify it against `SHA256SUMS` from the latest [GitHub Release](https://github.com/iivankin/platformd/releases). Set `PLATFORMD_VERSION` or `PLATFORMD_FORWARD_VERSION` to install a specific release.

## Development

Go 1.26 and Bun 1.3.14 are used by the current toolchain.

```bash
bun --cwd=_frontend install --frozen-lockfile
make check
make test
make build
```

Release builds and their signed manifests are produced by the release workflow. Never commit installation credentials, private keys, API tokens, or backup secrets.

### UI development with mock data

Run the frontend by itself with Bun's local server and an in-memory mock API:

```bash
bun --cwd=_frontend run dev:mock
```

The default `demo` scenario includes a project, managed resources, Registry
images, backups, tokens, certificates, logs, and audit events. Mutations update
the in-memory state until the server restarts. The browser UI hot reloads when
frontend files change.

Two additional scenarios cover empty and failed states:

```bash
bun --cwd=_frontend run dev:mock:empty
bun --cwd=_frontend run dev:mock:error
```

Use `PORT=3200` to change the default `http://127.0.0.1:3100` address, or pass
`--scenario demo|empty|error` to `dev:mock` directly.

## License

platformd is available under the [Apache License 2.0](LICENSE).
