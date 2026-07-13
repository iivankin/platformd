# platformd

`platformd` is a lightweight application platform for a single VPS. It runs prebuilt OCI images and provides the deployment and data services needed to operate them from one web interface.

It is deliberately single-node: no Kubernetes, no cluster coordination, and no image builds on the server.

## What is included

- Projects, services, custom domains, private project DNS, and automatic image updates
- Embedded OCI Registry and encrypted private S3-compatible object storage
- Managed PostgreSQL and Redis with data browsers, version changes, and S3 backups
- Service logs, container terminals, and a passphrase-protected host terminal
- Cloudflare Access authentication, API tokens, a REST API, and MCP
- HTTPS ingress, container networking, firewall isolation, and self-update

The control plane, admin UI, and private container runtime are distributed as one release executable.

## Requirements

- A dedicated `amd64` VPS running Debian 13 or Ubuntu 24.04
- Root access, systemd 255+, cgroup v2, OverlayFS, nftables, and a free TCP port 443
- A Cloudflare-proxied admin hostname, Cloudflare Access application, and matching Origin certificate

## Install

Download `platformd-linux-amd64` from [GitHub Releases](https://github.com/iivankin/platformd/releases), then run:

```bash
chmod +x platformd-linux-amd64
sudo ./platformd-linux-amd64 init
```

`init` validates the host and asks for the admin hostname, Cloudflare Access settings, Origin certificate, and console passphrase. It also prints the master recovery key once; save it outside the VPS. Further configuration happens in the admin UI.

## Development

Go 1.26 and Bun 1.3.14 are used by the current toolchain.

```bash
bun --cwd=_frontend install --frozen-lockfile
make check
make test
make build
```

Release builds and their signed manifests are produced by the release workflow. Never commit installation credentials, private keys, API tokens, or backup secrets.
