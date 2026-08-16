# Sentry SDK conformance

The harness builds disposable language-client images and sends real Sentry SDK
traffic through a running platformd service Sentry gateway. It also reads the
result through platformd's common Automation API. This intentionally exercises
the same trusted service routing and common authorization used in production;
the private Rust process has no standalone app catalog, API tokens, or MCP
server.

Release CI obtains both DSNs from the test gateway's `/configuration` endpoint.
That endpoint calls platformd's production DSN generator with a CUID2 service
identity, so every official client exercises the DSN shape returned by the
current platformd API.

Create one service, attach a public Sentry hostname reachable from the test
containers, rotate its artifact token, and create a read API token with access
to that service's project. Then run:

```sh
PLATFORMD_TELEMETRY_TEST_DSN='https://<dsn-public-key>@app.example.com/1' \
PLATFORMD_TELEMETRY_TEST_SERVICE_ID='<service-id>' \
PLATFORMD_TELEMETRY_TEST_ARTIFACT_TOKEN='ptel_artifact_…' \
PLATFORMD_TELEMETRY_TEST_API_TOKEN='…' \
PLATFORMD_TELEMETRY_TEST_API_URL='https://admin.example.com/public/api/v1/projects/<project-id>/services/<service-id>/errors' \
  ./telemetry/conformance/run.sh
```

Set `CONFORMANCE_CASE` to a case name from `cases.tsv` to run only one client.
The full harness covers Python, Flask, Node.js, Express, the browser JavaScript
SDK, Node.js CPU profiling, Go, Rust, Java, .NET, PHP, Ruby, source maps,
Sentry protocol boundaries, replays, and minidumps. The profiling case verifies
the complete event-to-trace-to-profile link and decoded stack samples rather
than only checking that the SDK request was accepted.
Common API and MCP protocol/auth behavior is covered by the platformd Go test
suite rather than duplicated here.

For a local bridge whose container hostname is not resolvable from the host,
set `PLATFORMD_TELEMETRY_TEST_HOST_DSN` to the host-reachable equivalent.
