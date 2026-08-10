# error-tracker

`error-tracker` is a standalone Sentry-compatible ingestion and investigation
server. platformd can run the same binary and talk to its management API; no
platformd package is imported by this crate.

Durable state has two explicit owners:

- `<volume>/config.json` stores applications, upload credential verifiers,
  application webhook settings, and public API token verifiers.
- Embedded Tantivy indexes every received event, envelope item, replay segment,
  artifact, and current issue aggregate below `<volume>/index`.
- Binary content is content-addressed by SHA-256 below `<volume>/blobs`. A
  durable WAL below `<volume>/wal` makes the index and blob update recoverable.

The server is one Rust binary and one process. Tantivy, JavaScript source-map
decoding, ProGuard/R8 deobfuscation, native debug-file symbolication, minidump
processing, and Apple crash-report parsing are linked libraries. There are no
Quickwit or Symbolicator daemons to install or supervise. The crate keeps a
narrow management API so the same build can run behind platformd or as an
independent product.

## Run standalone

```sh
export ERROR_TRACKER_ADMIN_TOKEN="replace-with-at-least-24-random-characters"
docker compose up --build
```

`ERROR_TRACKER_ADMIN_TOKEN` is optional. Without it, the management API trusts
its network boundary and does not require authentication. The embedded UI is
enabled automatically when the token is present. `ERROR_TRACKER_UI_ENABLED`
has explicit precedence: set it to `true` to expose the UI without an admin
token, or `false` to hide the UI even when an admin token exists.

| Variable | Default | Purpose |
| --- | --- | --- |
| `ERROR_TRACKER_ADMIN_TOKEN` | unset | Optional management API bearer token; at least 24 visible ASCII characters without whitespace |
| `ERROR_TRACKER_UI_ENABLED` | inferred from admin token | Explicit `true`/`false` UI override |
| `ERROR_TRACKER_PUBLIC_URL` | `http://localhost:8080` | Authoritative external origin used to derive DSNs and public client endpoints at runtime |
| `ERROR_TRACKER_LISTEN` | `0.0.0.0:8080` | Shared ingestion, management, public API, MCP, and optional UI listener |
| `ERROR_TRACKER_VOLUME` | `/data` | Required durable configuration, index, WAL, and blob storage |
| `ERROR_TRACKER_SLUG` | `error-tracker` | Sentry organization slug |
| `RUST_LOG` | `warn,error_tracker=info` | Rust tracing filter; dependencies stay quiet by default |

Create the first app through the management API:

```sh
curl -fsS http://localhost:8080/api/v1/apps \
  -H "Authorization: Bearer $ERROR_TRACKER_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Web","slug":"web"}'
```

The response contains the public DSN and a one-time artifact upload token. The
same admin API exposes issues, events, replays, uploaded artifacts, and per-app
webhooks. Set `ERROR_TRACKER_PUBLIC_URL` to the externally reachable origin and
restart the process after changing it. DSNs are derived from that value and the
application key/project ID; neither the public URL nor DSNs are persisted.

Tracker-wide or application-scoped API tokens are created in the UI or through
`POST /api/v1/tokens`. Their secret is returned once and only its verifier is
stored in `config.json`. The `read` role can query visible apps, issues, events,
replays, and artifacts. The `admin` role can also perform mutations inside its
scope. The same bearer credential works with:

```text
https://errors.example.com/public/api/v1
https://errors.example.com/public/mcp
```

The MCP endpoint implements stateless Streamable HTTP in the same binary; it
does not start a worker or sidecar.

The `/data` volume is mandatory. Back up the complete volume while the
container is stopped or quiesced; `config.json`, `index`, `blobs`, and `wal`
must be restored together.

## Development

```bash
bun --cwd=_frontend run dev:mock:error-tracker
bun --cwd=_frontend run build:web
cargo test --manifest-path error-tracker/Cargo.toml
cargo clippy --manifest-path error-tracker/Cargo.toml --all-targets -- -D warnings
```

The React UI lives under `_frontend/web/error-tracker` so platformd and the
standalone tracker share one Bun, Tailwind, and shadcn toolchain. The frontend
build writes self-contained assets to `error-tracker/src/web/dist`; Rust embeds
those generated files in the release binary. The Error Tracker mock runs at
`http://127.0.0.1:3101`, supports the UI's read and mutation flows, and resets
its in-memory state whenever the Bun process restarts.

The conformance harness sends real SDK traffic from Python, Flask, Node.js,
Express, Go, Rust, Java, .NET, PHP, and Ruby containers. It also uploads a real
JavaScript source map with `sentry-cli` and verifies the symbolicated frame:

```bash
ERROR_TRACKER_PUBLIC_URL=http://host.docker.internal:8080 \
ERROR_TRACKER_ADMIN_TOKEN="$ERROR_TRACKER_ADMIN_TOKEN" \
docker compose -f error-tracker/compose.yaml up --build

ERROR_TRACKER_URL=http://127.0.0.1:8080 \
ERROR_TRACKER_CONTAINER_URL=http://host.docker.internal:8080 \
ERROR_TRACKER_ADMIN_TOKEN="$ERROR_TRACKER_ADMIN_TOKEN" \
./error-tracker/conformance/run.sh
```
