# SDK conformance

The harness sends real events through official Sentry SDKs and verifies the
stored event, including the SDK metadata and a case-specific tag. It
covers generic clients plus Flask, Express, and Go `net/http` integration paths.

Start a disposable tracker with a public URL that is reachable from the client
containers, then run:

```sh
ERROR_TRACKER_PUBLIC_URL=http://host.docker.internal:8080 \
ERROR_TRACKER_ADMIN_TOKEN=replace-with-at-least-24-characters \
  docker compose -f error-tracker/compose.yaml up --build

ERROR_TRACKER_ADMIN_TOKEN=replace-with-at-least-24-characters \
  ./error-tracker/conformance/run.sh
```

On Docker Desktop, containers reach the tracker through
`host.docker.internal`. Override `ERROR_TRACKER_URL` for the management API or
`ERROR_TRACKER_CONTAINER_URL` for the DSN visible inside client containers.
Set `CONFORMANCE_CASE=express` to execute one SDK case, or
`CONFORMANCE_CASE=protocol` for the built-in envelope, compression, replay,
minidump, public-token, and MCP checks.

Run this against a disposable tracker. The configured public URL must equal
`ERROR_TRACKER_CONTAINER_URL` because
`sentry-cli` follows the absolute chunk-upload address advertised by the
tracker. The harness leaves its created applications and ingested conformance
data in place for inspection.

Every direct SDK and framework dependency is pinned exactly in its client
image. Adding another framework means adding a row to `cases.tsv` and teaching
the corresponding client to emit the tagged event; the assertion stays shared.
