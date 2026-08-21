# platformd telemetry

`platformd-telemetry` is platformd's private Rust data plane. platformd starts
one process per installation and reaches it over loopback. It has no standalone
configuration catalog, user-facing API, MCP server, Docker image, HTML, or
frontend assets. The common platformd API, MCP server, SQLite state, and web UI
own all service-facing behavior.

The process accepts Sentry traffic and OTLP logs, traces, and metrics. chDB,
source-map decoding, ProGuard/R8 deobfuscation, native symbolication, minidump
processing, and Apple crash-report parsing are linked into the binary; no
Quickwit, Symbolicator, collector, or sidecar is required.

Generative AI spans using current OpenTelemetry `gen_ai.*` conventions or AI
SDK telemetry are normalized during ingest. Agent/model/tool roles, token and
prompt-cache usage, reported cost, time to first output, throughput, and a
bounded prompt/response/tool search document stay queryable in chDB. The web UI
uses reported `operation.cost` when present and labels catalog-derived model
pricing as an estimate.

An AI span does not replace the enclosing distributed trace. Trace summaries
keep the real root span and report how many agent runs occurred inside it. The
web UI scopes each agent run to that agent span and its descendants, while the
normal waterfall retains HTTP, database, queue, and sibling-agent context.

## Identity and state

platformd's SQLite `services.id` is the only service identity. An internal DSN
is derived as `<scheme>://<service-id>@<hostname>/1`; a public DSN uses the
same root-path shape. Telemetry can share an application hostname because
platformd reserves only exact Sentry SDK and artifact-upload routes. The public
key is the CUID2 service ID and Sentry's protocol project ID is the constant
`1`. The trusted gateway resolves the host to a service and injects that exact
identity before forwarding traffic, so a client cannot select another service
by changing its DSN.

SQLite stores artifact-token verifiers, webhook configuration, encrypted
webhook secrets, public hostnames, and all other control-plane state. chDB never
stores a second project or service catalog.

## Browser traces

The service telemetry settings can reserve one exact public path for OTLP
HTTP/protobuf traces. The generated browser example initializes OpenTelemetry
before the application, exports with a batch span processor, and instruments
document load, user interactions, fetch, and XMLHttpRequest. It also provides a
`traced()` helper for framework-specific route loaders and important business
operations that generic browser instrumentation cannot identify.

Only explicitly selected API origins receive W3C trace propagation headers.
Cross-origin APIs must allow `traceparent` and `tracestate` in their CORS
configuration. The public trace exporter path itself supports browser
preflight requests and is excluded from fetch/XHR instrumentation.

Sentry remains responsible for errors and replay. Its generated browser setup
disables Sentry transaction export and uses a global
`beforeSend` hook to copy the active OpenTelemetry trace and span IDs into an
error event. Errors outside an active OpenTelemetry span remain valid Sentry
events but cannot be attached to a trace.

## Storage and backups

- `<volume>/chdb` stores Sentry events and replays plus OTLP logs, traces, and
  metrics. Mutable issue rows use explicit revisions in the same database.
- `<volume>/blobs` stores content-addressed envelope payloads and debug
  artifacts.
- `<volume>/GeoIP-City.mmdb` is downloaded only for non-Cloudflare deployments
  that select database GeoIP. platformd uses Cloudflare's visitor location
  headers.

The telemetry resource backup contains a native chDB backup plus referenced
blobs. The normal platformd control backup contains SQLite, including webhook
and credential state. There is no `config.json` and no duplicated catalog to
reconcile after restore.

## Private process configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PLATFORMD_TELEMETRY_VOLUME` | `/data` | Durable telemetry volume |
| `PLATFORMD_TELEMETRY_SENTRY_LISTEN` | `127.0.0.1:4319` | Sentry and private query listener |
| `PLATFORMD_TELEMETRY_OTLP_GRPC_LISTEN` | `127.0.0.1:4317` | OTLP/gRPC listener |
| `PLATFORMD_TELEMETRY_OTLP_HTTP_LISTEN` | `127.0.0.1:4318` | OTLP HTTP/protobuf and JSON listener |
| `PLATFORMD_TELEMETRY_GEOIP_SOURCE` | `database` | `database` or `cloudflare` |
| `RUST_LOG` | `warn,platformd_telemetry=info` | Rust tracing filter |

Users configure service DSNs, public domains, artifact uploads, webhooks, API
tokens, and MCP through platformd.

## Development

```bash
bun --cwd=_frontend run dev:mock
cargo test --manifest-path telemetry/Cargo.toml
cargo clippy --manifest-path telemetry/Cargo.toml --all-targets -- -D warnings
```

The platformd frontend mock includes the service Errors tab. The conformance
harness sends official Sentry SDK traffic through a running platformd gateway;
see [`conformance/README.md`](conformance/README.md).
