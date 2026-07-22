# platformd

`platformd` is a self-hosted application platform for one VPS. It deploys services, PostgreSQL, Redis, object storage, domains, backups, logs, metrics, terminals, and private networking from one project canvas.

The server, admin UI, and container runtime are distributed as one `platformd` release executable. `platformd-forward` is a small local helper that connects short-lived API/MCP port-forward tickets to a localhost TCP port.

## Demo

Explore a running installation at [platformd-demo.ivnkn.xyz](https://platformd-demo.ivnkn.xyz).

## Requirements

- A dedicated `amd64` VPS running Debian 13 or Ubuntu 24.04
- Root access, systemd 255+, cgroup v2, OverlayFS, nftables, and a free TCP port 443
- A Cloudflare-proxied admin hostname, Cloudflare Access application, and matching Origin certificate

## Install

### 1. Prepare Cloudflare

`platformd` serves HTTPS directly from the VPS on port `443`; a Cloudflare
Tunnel is not required. Before running the installer:

1. Add the domain to Cloudflare, then create a **Proxied** `A` record for the
   admin hostname (for example, `admin.example.com`) pointing to the VPS public
   IPv4 address. Add a proxied `AAAA` record too if the VPS has public IPv6.
   See Cloudflare's [DNS record guide](https://developers.cloudflare.com/dns/manage-dns-records/how-to/create-dns-records/)
   and [proxy status reference](https://developers.cloudflare.com/dns/proxy-status/).
2. In **SSL/TLS**, set the encryption mode to **Full (strict)**. Create a
   Cloudflare Origin CA certificate in PEM format that covers the admin
   hostname. A wildcard such as `*.example.com` is useful when the same zone
   will host services later. Save both the certificate and private key when
   Cloudflare displays them; the private key is shown only once. Follow the
   [Origin CA guide](https://developers.cloudflare.com/ssl/origin-configuration/origin-ca/).
3. In **Zero Trust → Access controls → Applications**, create a
   **Self-hosted and private** application with the admin hostname as a public
   hostname. Add an `Allow` policy for the users or identity-provider groups
   that may administer the VPS. Cloudflare Access denies users who do not match
   an Allow policy. See [Create an Access application](https://developers.cloudflare.com/learning-paths/clientless-access/access-application/create-access-app/).
4. Record the values required by `platformd init`:
   - **Team domain** — `<team>.cloudflareaccess.com`, shown under Zero Trust
     settings. Do not include `https://`. See Cloudflare's
     [team domain explanation](https://developers.cloudflare.com/cloudflare-one/faq/getting-started-faq/#what-is-a-team-domainteam-name).
   - **Application Audience (AUD) Tag** — open the Access application, then copy
     it from **Additional settings**. See [Get your AUD tag](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/#get-your-aud-tag).

Copy the Origin CA files to the VPS and restrict the private key before the
bootstrap reads it:

```bash
scp cloudflare-origin.pem cloudflare-origin-key.pem root@VPS_IP:/root/
ssh root@VPS_IP
chmod 600 /root/cloudflare-origin.pem /root/cloudflare-origin-key.pem
```

### 2. Install platformd

On the VPS, install the latest release and start the interactive bootstrap:

```bash
curl -fsSL https://raw.githubusercontent.com/iivankin/platformd/main/install.sh | sudo sh -s -- platformd
sudo platformd init
```

Use the values prepared above when prompted:

| Prompt | Example |
| --- | --- |
| Admin hostname | `admin.example.com` |
| Cloudflare Access team domain | `example.cloudflareaccess.com` |
| Cloudflare Access application AUD | The AUD copied from the application |
| Origin certificate PEM path | `/root/cloudflare-origin.pem` |
| Origin private key PEM path | `/root/cloudflare-origin-key.pem` |
| Console passphrase | A new passphrase for emergency server consoles |

`init` validates the host, admin hostname, Access settings, and certificate/key
pair before enabling the service. It prints the master recovery key once. Save
that key in a password manager or another location outside the VPS and confirm
the prompt only after doing so. After a successful bootstrap, remove the
temporary certificate files from `/root`; platformd keeps the private key
encrypted in its state database.

Open `https://admin.example.com` and authenticate through Cloudflare Access.
Further certificates, public service domains, backup storage, GitHub, and
Cloudflare API integration are configured in the admin UI.

Keep the Access application scoped to the admin hostname. If GitHub webhooks or
other machine callbacks are needed, configure a separate public API hostname in
platformd Settings rather than putting those callbacks behind the interactive
Access login.

### 3. Install the optional port-forward helper

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

The same mock UI can run in Docker:

```bash
docker build -t platformd-ui-mock _frontend
docker run --rm -p 3100:3100 platformd-ui-mock
```

The final image runs a compiled standalone executable as a non-root user and
does not contain Bun, source files, or `node_modules`.

Select another fixture with `-e MOCK_SCENARIO=empty` or
`-e MOCK_SCENARIO=error`.

## License

platformd is available under the [Apache License 2.0](LICENSE).
