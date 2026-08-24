import { CopyButton } from "@/errors/settings-common";
import { HighlightedSnippet } from "@/snippet-code";

import {
  browserTelemetryInstall,
  browserTelemetrySetup,
} from "./browser-otel-setup";

export const BrowserOTELGuide = ({
  endpoint,
  sentryDsn,
  sentryTunnel,
  serviceName,
}: {
  endpoint: string;
  sentryDsn?: string;
  sentryTunnel?: string;
  serviceName: string;
}) => {
  const install = browserTelemetryInstall(Boolean(sentryDsn));
  const source = browserTelemetrySetup({
    endpoint,
    sentryDsn,
    sentryTunnel,
    serviceName,
  });
  const setup = `${install}\n\n${source}`;

  return (
    <div className="mt-5 max-w-4xl">
      <div className="border-y border-border">
        <div className="flex min-h-11 items-center justify-between gap-4 border-b border-border px-3">
          <code className="min-w-0 overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-foreground/75">
            {install}
          </code>
          <CopyButton value={setup} />
        </div>
        <HighlightedSnippet
          className="max-h-[34rem] border-0 bg-transparent px-3 py-4 pr-3"
          language="typescript"
          value={source}
        />
      </div>

      <div className="mt-3 max-w-3xl space-y-2 text-[9px] leading-4 text-muted-foreground">
        <p>
          Import telemetry.ts before the application entry point. It captures
          the initial document load, Web Vitals, user interactions, fetch and
          XHR requests, batches exports, sends OTLP HTTP/protobuf with gzip when
          compression reduces the payload, and keeps async span context
          available to Sentry. Browsers without CompressionStream fall back to
          uncompressed protobuf.
        </p>
        <p>
          Replace api.example.com with each API origin that should receive W3C
          trace headers. Cross-origin APIs must allow traceparent and tracestate
          in CORS; do not use a match-all expression for third-party URLs.
        </p>
        <p>
          Automatic instrumentation cannot understand framework-specific SPA
          routes or business boundaries. Wrap route loaders, mutations, and
          important workflows with traced() so their async work and errors stay
          under one active span.
        </p>
        {sentryDsn ? (
          <p>
            Sentry tracing stays disabled. Error events and replay still use
            Sentry; beforeSend attaches the active OTel trace and span IDs so
            platformd can place the error in the same waterfall.
          </p>
        ) : (
          <p>
            Add a public Sentry domain to include the global Sentry error bridge
            in this example.
          </p>
        )}
      </div>
    </div>
  );
};
