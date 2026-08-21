interface BrowserTelemetrySetup {
  endpoint: string;
  sentryDsn?: string;
  sentryTunnel?: string;
  serviceName: string;
}

const otelPackages = [
  "@opentelemetry/api",
  "@opentelemetry/context-zone",
  "@opentelemetry/core",
  "@opentelemetry/instrumentation",
  "@opentelemetry/instrumentation-document-load",
  "@opentelemetry/instrumentation-fetch",
  "@opentelemetry/instrumentation-user-interaction",
  "@opentelemetry/instrumentation-xml-http-request",
  "@opentelemetry/otlp-transformer",
  "@opentelemetry/resources",
  "@opentelemetry/sdk-trace-base",
  "@opentelemetry/sdk-trace-web",
  "@opentelemetry/semantic-conventions",
];

const quoted = (value: string) => JSON.stringify(value);

export const browserSentryInstall =
  "npm install @sentry/browser @opentelemetry/api";

export const browserSentrySetup = (
  dsn: string,
  tunnel?: string
) => `import * as Sentry from "@sentry/browser";
import {
  context,
  isSpanContextValid,
  SpanStatusCode,
  trace,
} from "@opentelemetry/api";

Sentry.init({
  dsn: ${quoted(dsn)},
${tunnel ? `  tunnel: ${quoted(tunnel)},\n` : ""}  integrations: [
    Sentry.replayIntegration(),
  ],
  // OTel owns tracing. Sentry only sends errors and replay.
  tracesSampleRate: 0,
  beforeSendTransaction: () => null,
  replaysSessionSampleRate: 0.1,
  replaysOnErrorSampleRate: 1.0,
  beforeSend(event) {
    const activeSpan = trace.getSpan(context.active());
    const spanContext = activeSpan?.spanContext();
    if (!(activeSpan && spanContext && isSpanContextValid(spanContext))) {
      return event;
    }

    activeSpan.setStatus({ code: SpanStatusCode.ERROR });
    event.contexts = {
      ...event.contexts,
      trace: {
        ...event.contexts?.trace,
        trace_id: spanContext.traceId,
        span_id: spanContext.spanId,
      },
    };
    return event;
  },
});`;

export const browserTelemetryInstall = (includeSentry: boolean) =>
  `npm install ${includeSentry ? ["@sentry/browser", ...otelPackages].join(" ") : otelPackages.join(" ")}`;

const otelSetup = (
  endpoint: string,
  serviceName: string
) => `// telemetry.ts — import this before rendering the application.
import { SpanStatusCode, trace } from "@opentelemetry/api";
import { ZoneContextManager } from "@opentelemetry/context-zone";
import {
  ExportResultCode,
  type ExportResult,
} from "@opentelemetry/core";
import { registerInstrumentations } from "@opentelemetry/instrumentation";
import { DocumentLoadInstrumentation } from "@opentelemetry/instrumentation-document-load";
import { FetchInstrumentation } from "@opentelemetry/instrumentation-fetch";
import { UserInteractionInstrumentation } from "@opentelemetry/instrumentation-user-interaction";
import { XMLHttpRequestInstrumentation } from "@opentelemetry/instrumentation-xml-http-request";
import { ProtobufTraceSerializer } from "@opentelemetry/otlp-transformer";
import { defaultResource, resourceFromAttributes } from "@opentelemetry/resources";
import {
  AlwaysOnSampler,
  BatchSpanProcessor,
  ParentBasedSampler,
  type ReadableSpan,
  type SpanExporter,
} from "@opentelemetry/sdk-trace-base";
import { WebTracerProvider } from "@opentelemetry/sdk-trace-web";
import { ATTR_SERVICE_NAME } from "@opentelemetry/semantic-conventions";

const traceEndpoint = ${quoted(endpoint)};
const maximumKeepaliveBytes = 60 * 1024;

const gzipPayload = async (payload: Uint8Array) => {
  const body = Uint8Array.from(payload).buffer;
  if (typeof CompressionStream === "undefined") {
    return { body };
  }
  const compressed = await new Response(
    new Blob([body]).stream().pipeThrough(new CompressionStream("gzip"))
  ).arrayBuffer();
  return compressed.byteLength < body.byteLength
    ? { body: compressed, encoding: "gzip" }
    : { body };
};

class CompressedOTLPTraceExporter implements SpanExporter {
  private stopped = false;

  export(
    spans: ReadableSpan[],
    resultCallback: (result: ExportResult) => void
  ): void {
    if (this.stopped) {
      resultCallback({
        code: ExportResultCode.FAILED,
        error: new Error("OTLP exporter is shut down"),
      });
      return;
    }
    const payload = ProtobufTraceSerializer.serializeRequest(spans);
    if (!payload) {
      resultCallback({
        code: ExportResultCode.FAILED,
        error: new Error("Unable to encode OTLP traces"),
      });
      return;
    }
    void gzipPayload(payload)
      .then(({ body, encoding }) => {
        const headers: Record<string, string> = {
          "Content-Type": "application/x-protobuf",
        };
        if (encoding) {
          headers["Content-Encoding"] = encoding;
        }
        return fetch(traceEndpoint, {
          method: "POST",
          headers,
          body,
          // Fetch caps all in-flight keepalive bodies at 64 KiB. Leave room for
          // other unload requests and send larger trace batches normally.
          keepalive: body.byteLength <= maximumKeepaliveBytes,
          credentials: "omit",
        });
      })
      .then((response) => {
        if (!response.ok) {
          throw new Error(\`OTLP export failed with HTTP \${response.status}\`);
        }
        resultCallback({ code: ExportResultCode.SUCCESS });
      })
      .catch((error: unknown) => {
        resultCallback({
          code: ExportResultCode.FAILED,
          error: error instanceof Error ? error : new Error(String(error)),
        });
      });
  }

  shutdown(): Promise<void> {
    this.stopped = true;
    return Promise.resolve();
  }
}

// Add only API origins that should receive W3C traceparent/tracestate headers.
const apiTracePropagationTargets = [
  window.location.origin,
  new RegExp("^https://api[.]example[.]com/"),
];

const provider = new WebTracerProvider({
  resource: defaultResource().merge(
    resourceFromAttributes({ [ATTR_SERVICE_NAME]: ${quoted(serviceName)} })
  ),
  sampler: new ParentBasedSampler({ root: new AlwaysOnSampler() }),
  spanProcessors: [
    new BatchSpanProcessor(new CompressedOTLPTraceExporter()),
  ],
});

provider.register({ contextManager: new ZoneContextManager() });
registerInstrumentations({
  tracerProvider: provider,
  instrumentations: [
    new DocumentLoadInstrumentation(),
    new UserInteractionInstrumentation(),
    new FetchInstrumentation({
      clearTimingResources: true,
      ignoreUrls: [traceEndpoint],
      propagateTraceHeaderCorsUrls: apiTracePropagationTargets,
    }),
    new XMLHttpRequestInstrumentation({
      clearTimingResources: true,
      ignoreUrls: [traceEndpoint],
      propagateTraceHeaderCorsUrls: apiTracePropagationTargets,
    }),
  ],
});

const tracer = trace.getTracer(${quoted(serviceName)});

// Use for SPA route loaders and important business operations.
export const traced = <T>(name: string, operation: () => Promise<T>) =>
  tracer.startActiveSpan(name, async (span) => {
    try {
      return await operation();
    } catch (error) {
      if (error instanceof Error) {
        span.recordException(error);
      }
      span.setStatus({ code: SpanStatusCode.ERROR });
      throw error;
    } finally {
      span.end();
    }
  });`;

export const browserTelemetrySetup = ({
  endpoint,
  sentryDsn,
  sentryTunnel,
  serviceName,
}: BrowserTelemetrySetup) => {
  const telemetry = otelSetup(endpoint, serviceName);
  if (!sentryDsn) {
    return telemetry;
  }
  return `${telemetry}\n\n// sentry.ts — initialize after telemetry.ts.\n${browserSentrySetup(sentryDsn, sentryTunnel)}`;
};

export const publicSentryTunnel = (dsn?: string, path?: string) => {
  if (!(dsn && path)) {
    return;
  }
  return new URL(path, new URL(dsn).origin).toString();
};
