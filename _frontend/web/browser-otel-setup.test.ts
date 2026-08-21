import { describe, expect, test } from "bun:test";

import {
  browserSentrySetup,
  browserTelemetryInstall,
  browserTelemetrySetup,
  publicSentryTunnel,
} from "./browser-otel-setup";

describe("browser telemetry setup", () => {
  test("generates complete OTel instrumentation and the Sentry bridge", () => {
    const source = browserTelemetrySetup({
      endpoint: "https://app.example.com/otel/v1/traces",
      sentryDsn: "https://service@app.example.com/1",
      sentryTunnel: "https://app.example.com/client-report",
      serviceName: "checkout-web",
    });

    for (const expected of [
      "DocumentLoadInstrumentation",
      "UserInteractionInstrumentation",
      "FetchInstrumentation",
      "XMLHttpRequestInstrumentation",
      "BatchSpanProcessor",
      "CompressedOTLPTraceExporter",
      'new CompressionStream("gzip")',
      'headers["Content-Encoding"] = encoding',
      "keepalive: body.byteLength <= maximumKeepaliveBytes",
      "ParentBasedSampler",
      "ProtobufTraceSerializer",
      "startActiveSpan",
      "beforeSendTransaction: () => null",
      "trace_id: spanContext.traceId",
      "span_id: spanContext.spanId",
    ]) {
      expect(source).toContain(expected);
    }
    expect(source).toContain(
      'const traceEndpoint = "https://app.example.com/otel/v1/traces"'
    );
    expect(source).toContain('tunnel: "https://app.example.com/client-report"');
    expect(source).toContain('[ATTR_SERVICE_NAME]: "checkout-web"');
  });

  test("keeps the OTel-only example independent from Sentry", () => {
    const source = browserTelemetrySetup({
      endpoint: "https://otel.example.com/v1/traces",
      serviceName: "web",
    });

    expect(source).not.toContain("Sentry.init");
    expect(browserTelemetryInstall(false)).not.toContain("@sentry/browser");
    expect(browserTelemetryInstall(true)).toContain("@sentry/browser");
    expect(browserTelemetryInstall(false)).toContain(
      "@opentelemetry/otlp-transformer"
    );
    expect(browserTelemetryInstall(false)).not.toContain(
      "@opentelemetry/exporter-trace-otlp-http"
    );
  });

  test("builds the Sentry tunnel from the DSN origin", () => {
    expect(
      publicSentryTunnel(
        "https://service@errors.example.com/1",
        "/client-report"
      )
    ).toBe("https://errors.example.com/client-report");
    expect(publicSentryTunnel(undefined, "/client-report")).toBeUndefined();
  });

  test("does not emit an empty tunnel option", () => {
    expect(
      browserSentrySetup("https://service@errors.example.com/1")
    ).not.toContain("tunnel:");
  });
});
