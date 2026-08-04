# platformd port forward

Open a short-lived localhost TCP tunnel from a GitHub Actions runner to a
running platformd service, PostgreSQL database, object store, or Redis instance.

## Usage

```yaml
jobs:
  integration:
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      id-token: write
    steps:
      - name: Open database tunnel
        id: database
        uses: iivankin/platformd/actions/port-forward@v1
        with:
          url: https://admin.example.com
          project: my-project
          resource: database
          port: 5432
          local-port: 15432
          connection-url: ${{ secrets.POSTGRES_URL }}

      - name: Use database
        run: |
          bun run migrate
```

By default the action authenticates with GitHub Actions OIDC against the
resource port-forward allowlist (service, PostgreSQL, Redis, or object store).
Pass `token` to use an admin API token instead. When using OIDC, set
`permissions: id-token: write` and configure the repository (and optional
workflows) in the resource settings Port forward section.

By default the action downloads the matching `platformd-forward` release,
verifies it against the release `SHA256SUMS`, creates a temporary ticket, starts
the tunnel, and stops the helper automatically at the end of the job. Set
`binary-path` to run a local helper instead; that path skips the release download
and checksum verification.

`url` must be the HTTPS origin of the platformd admin hostname. It is not a
secret; a repository variable is fine. Cloudflare Access must bypass `/public/*`
on that hostname.

Object store tunnels always target the project S3 endpoint on port `9000`.

## Inputs

| Input | Required | Default | Description |
| --- | --- | --- | --- |
| `url` | yes | | platformd admin hostname HTTPS origin |
| `token` | no | | admin API token; when omitted, uses GitHub Actions OIDC |
| `project` | yes | | target project name |
| `resource` | yes | | target resource name; platformd detects its kind |
| `port` | yes | | target TCP port |
| `local-port` | no | target port | localhost TCP port |
| `expires-in-seconds` | no | `3600` | ticket lifetime, from 60 to 28800 seconds |
| `platformd-version` | no | `latest` | exact stable helper version or `latest`; ignored when `binary-path` is set |
| `binary-path` | no | | local `platformd-forward` path; skips download and SHA256 verification |
| `connection-url` | no | | PostgreSQL or Redis URL to route through the tunnel |
| `connection-env` | no | kind default | exported variable name |

When `connection-url` is set, the action preserves its credentials, database,
query, and protocol, replaces only the host and port, masks both URL values,
and exports the result to later steps. PostgreSQL defaults to `POSTGRES_URL`;
Redis defaults to `REDIS_URL`. Use `connection-env` to override the name.

For reproducible workflows, set `platformd-version` to an exact release and pin
the action itself to a full commit SHA. The `v1` tag is convenient but mutable.

## Outputs

| Output | Description |
| --- | --- |
| `host` | `127.0.0.1` |
| `port` | selected local port |
| `expires-at` | ticket expiration time |

The ticket is masked in workflow logs and is never exposed as an action output.
The platformd API currently has no ticket revocation endpoint, so a stopped
tunnel's ticket remains valid until its configured expiration.

## Supported runners

- Linux x64 and arm64
- macOS x64 and arm64

Windows is not supported because platformd does not publish a Windows forward
helper.

## Development

Node.js 24 is required. Sources are TypeScript; committed `dist/` is the
CommonJS bundle GitHub Actions executes.

```bash
npm ci
npm run check
```
