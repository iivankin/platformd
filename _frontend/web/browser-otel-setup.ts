interface BrowserTelemetrySetup {
  conformance?: boolean;
  endpoint: string;
  sentryDsn?: string;
  sentryTunnel?: string;
  serviceName: string;
}

const sentryBrowserPackage = ["@sentry/browser", "10.70.0"] as const;
const otelPackages = [
  ["@opentelemetry/api", "1.9.1"],
  ["@opentelemetry/api-logs", "0.221.0"],
  ["@opentelemetry/browser-instrumentation", "0.7.0"],
  ["@opentelemetry/core", "2.10.0"],
  ["@opentelemetry/instrumentation", "0.221.0"],
  ["@opentelemetry/instrumentation-document-load", "0.66.0"],
  ["@opentelemetry/instrumentation-fetch", "0.221.0"],
  ["@opentelemetry/instrumentation-xml-http-request", "0.221.0"],
  ["@opentelemetry/otlp-transformer", "0.221.0"],
  ["@opentelemetry/resources", "2.10.0"],
  ["@opentelemetry/sdk-logs", "0.221.0"],
  ["@opentelemetry/sdk-trace-base", "2.10.0"],
  ["@opentelemetry/sdk-trace-web", "2.10.0"],
  ["@opentelemetry/semantic-conventions", "1.43.0"],
] as const;

const quoted = (value: string) => JSON.stringify(value);
const packageSpec = ([name, version]: readonly [string, string]) =>
  `${name}@${version}`;

export const browserSentryInstall = `npm install ${packageSpec(sentryBrowserPackage)} ${packageSpec(otelPackages[0])}`;

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
  `npm install ${[
    ...(includeSentry ? [sentryBrowserPackage] : []),
    ...otelPackages,
  ]
    .map(packageSpec)
    .join(" ")}`;

const otelSetup = (
  endpoint: string,
  serviceName: string,
  conformance: boolean
) => `// telemetry.ts — import this before rendering the application.
import {
  context,
  propagation,
  ROOT_CONTEXT,
  SpanStatusCode,
  trace,
} from "@opentelemetry/api";
import type {
  Context,
  TextMapGetter,
  TextMapSetter,
} from "@opentelemetry/api";
import { logs, type LogRecord } from "@opentelemetry/api-logs";
import { WebVitalsInstrumentation } from "@opentelemetry/browser-instrumentation/experimental/web-vitals";
import {
  ExportResultCode,
  type ExportResult,
} from "@opentelemetry/core";
import { registerInstrumentations } from "@opentelemetry/instrumentation";
import { DocumentLoadInstrumentation } from "@opentelemetry/instrumentation-document-load";
import { FetchInstrumentation } from "@opentelemetry/instrumentation-fetch";
import { XMLHttpRequestInstrumentation } from "@opentelemetry/instrumentation-xml-http-request";
import {
  ProtobufLogsSerializer,
  ProtobufTraceSerializer,
} from "@opentelemetry/otlp-transformer";
import { defaultResource, resourceFromAttributes } from "@opentelemetry/resources";
import {
  BatchLogRecordProcessor,
  LoggerProvider,
  type LogRecordProcessor,
  type LogRecordExporter,
  type ReadableLogRecord,
  type SdkLogRecord,
} from "@opentelemetry/sdk-logs";
import {
  AlwaysOnSampler,
  BatchSpanProcessor,
  ParentBasedSampler,
  type ReadableSpan,
  type Span,
  type SpanExporter,
  type SpanProcessor,
} from "@opentelemetry/sdk-trace-base";
import { WebTracerProvider } from "@opentelemetry/sdk-trace-web";
import { ATTR_SERVICE_NAME } from "@opentelemetry/semantic-conventions";

const otlpEndpoint = ${quoted(endpoint)};
const traceEndpoint = otlpEndpoint + "/v1/traces";
const logEndpoint = otlpEndpoint + "/v1/logs";
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

const exportPayload = (
  endpoint: string,
  payload: Uint8Array | undefined,
  resultCallback: (result: ExportResult) => void
) => {
  if (!payload) {
    resultCallback({
      code: ExportResultCode.FAILED,
      error: new Error("Unable to encode OTLP payload"),
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
      return fetch(endpoint, {
        method: "POST",
        headers,
        body,
        // Fetch caps all in-flight keepalive bodies at 64 KiB. Leave room for
        // other unload requests and send larger batches normally.
        keepalive: body.byteLength <= maximumKeepaliveBytes,
        credentials: "omit",
      });
    })
    .then((response) => {
      if (!response.ok) {
        throw new Error("OTLP export failed with HTTP " + response.status);
      }
      resultCallback({ code: ExportResultCode.SUCCESS });
    })
    .catch((error: unknown) => {
      resultCallback({
        code: ExportResultCode.FAILED,
        error: error instanceof Error ? error : new Error(String(error)),
      });
    });
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
    exportPayload(traceEndpoint, payload, resultCallback);
  }

  shutdown(): Promise<void> {
    this.stopped = true;
    return Promise.resolve();
  }
}

class CompressedOTLPLogExporter implements LogRecordExporter {
  private stopped = false;

  export(
    records: ReadableLogRecord[],
    resultCallback: (result: ExportResult) => void
  ): void {
    if (this.stopped) {
      resultCallback({
        code: ExportResultCode.FAILED,
        error: new Error("OTLP exporter is shut down"),
      });
      return;
    }
    const payload = ProtobufLogsSerializer.serializeRequest(records);
    exportPayload(logEndpoint, payload, resultCallback);
  }

  forceFlush(): Promise<void> {
    return Promise.resolve();
  }

  shutdown(): Promise<void> {
    this.stopped = true;
    return Promise.resolve();
  }
}
${
  conformance
    ? `
let resolveWebVitalEmitted!: () => void;
const webVitalEmitted = new Promise<void>((resolve) => {
  resolveWebVitalEmitted = resolve;
});

class WebVitalConformanceProcessor implements LogRecordProcessor {
  onEmit(record: SdkLogRecord): void {
    if (record.eventName === "browser.web_vital") {
      resolveWebVitalEmitted();
    }
  }

  forceFlush(): Promise<void> {
    return Promise.resolve();
  }

  shutdown(): Promise<void> {
    return Promise.resolve();
  }
}
`
    : ""
}

let documentLoadContext: Context | undefined;
let webVitalsInstrumentation: WebVitalsInstrumentation | undefined;

class DocumentLoadContextProcessor implements SpanProcessor {
  onStart(span: Span): void {
    if (
      span.name === "documentLoad" &&
      span.instrumentationScope.name ===
        "@opentelemetry/instrumentation-document-load"
    ) {
      documentLoadContext = trace.setSpan(ROOT_CONTEXT, span);
      // registerInstrumentations is still assigning providers while this span
      // starts. Defer Web Vitals until its logger provider is attached.
      queueMicrotask(() => webVitalsInstrumentation?.enable());
    }
  }

  onEnd(): void {}

  forceFlush(): Promise<void> {
    return Promise.resolve();
  }

  shutdown(): Promise<void> {
    return Promise.resolve();
  }
}

// Add only API origins that should receive W3C traceparent/tracestate headers.
const apiTracePropagationTargets = [
  window.location.origin,
  new RegExp("^https://api[.]example[.]com/"),
];

const resource = defaultResource().merge(
  resourceFromAttributes({ [ATTR_SERVICE_NAME]: ${quoted(serviceName)} })
);
const provider = new WebTracerProvider({
  resource,
  sampler: new ParentBasedSampler({ root: new AlwaysOnSampler() }),
  spanProcessors: [
    new DocumentLoadContextProcessor(),
    new BatchSpanProcessor(new CompressedOTLPTraceExporter()),
  ],
});
const loggerProvider = new LoggerProvider({
  resource,
  processors: [
${conformance ? "    new WebVitalConformanceProcessor(),\n" : ""}    new BatchLogRecordProcessor({
      exporter: new CompressedOTLPLogExporter(),
    }),
  ],
});

provider.register();
logs.setGlobalLoggerProvider(loggerProvider);
webVitalsInstrumentation = new WebVitalsInstrumentation({
  enabled: false,
  applyCustomLogRecordData(logRecord: LogRecord) {
    if (documentLoadContext) {
      logRecord.context = documentLoadContext;
    }
  },
});
webVitalsInstrumentation.setTracerProvider(provider);
webVitalsInstrumentation.setLoggerProvider(loggerProvider);
registerInstrumentations({
  tracerProvider: provider,
  instrumentations: [
    new DocumentLoadInstrumentation(),
    new FetchInstrumentation({
      clearTimingResources: true,
      ignoreUrls: [traceEndpoint, logEndpoint],
      propagateTraceHeaderCorsUrls: apiTracePropagationTargets,
    }),
    new XMLHttpRequestInstrumentation({
      clearTimingResources: true,
      ignoreUrls: [traceEndpoint, logEndpoint],
      propagateTraceHeaderCorsUrls: apiTracePropagationTargets,
    }),
  ],
});

const tracer = trace.getTracer(${quoted(serviceName)});

type TraceHeaders = Record<string, string>;
type BrowserFetch = (
  input: RequestInfo | URL,
  init?: RequestInit
) => Promise<Response>;

const traceHeadersGetter: TextMapGetter<Headers> = {
  get: (carrier, key) => carrier.get(key) ?? undefined,
  keys: (carrier) => [...carrier.keys()],
};
const traceHeadersSetter: TextMapSetter<TraceHeaders> = {
  set(carrier, key, value) {
    carrier[key] = value;
  },
};

export const captureActiveTraceHeaders = () => {
  const headers: TraceHeaders = {};
  propagation.inject(context.active(), headers, traceHeadersSetter);
  return headers;
};

// Pass this to libraries that accept a custom fetch implementation. It restores
// the captured parent after native async/await has lost the active context.
export const fetchWithTraceContext: BrowserFetch = (input, init) => {
  const headers = new Headers(init?.headers);
  const parentContext = propagation.extract(
    context.active(),
    headers,
    traceHeadersGetter
  );
  return context.with(parentContext, () =>
    globalThis.fetch(input, { ...init, headers })
  );
};

// Call directly from event handlers, SPA route loaders, and important workflows.
export const traced = <T>(
  name: string,
  operation: (traceHeaders: TraceHeaders) => Promise<T>
) =>
  tracer.startActiveSpan(name, async (span) => {
    try {
      return await operation(captureActiveTraceHeaders());
    } catch (error) {
      if (error instanceof Error) {
        span.recordException(error);
      }
      span.setStatus({ code: SpanStatusCode.ERROR });
      throw error;
    } finally {
      span.end();
    }
  });

// Example:
// void traced("checkout.submit", (headers) =>
//   fetchWithTraceContext("/api/checkout", { method: "POST", headers })
// );`;

export const browserTelemetrySetup = ({
  conformance = false,
  endpoint,
  sentryDsn,
  sentryTunnel,
  serviceName,
}: BrowserTelemetrySetup) => {
  const telemetry = otelSetup(endpoint, serviceName, conformance);
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
