import type { ServiceTraceSpan } from "@/api";
import { otlpTextAttributes, otlpValueText } from "@/otlp";

export interface TraceContextValue {
  label: string;
  value: string;
}

export interface TraceContextGroup {
  label: string;
  values: TraceContextValue[];
}

export interface TraceSpanEvent {
  attributes: TraceContextValue[];
  name: string;
  timeUnixNano?: string;
}

export interface TraceSpanLink {
  attributes: TraceContextValue[];
  spanID?: string;
  traceID?: string;
}

const record = (value: unknown): Record<string, unknown> | undefined =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;

export const otlpAttributes = (value: unknown): TraceContextValue[] =>
  otlpTextAttributes(value).map(({ key, value: entry }) => ({
    label: key,
    value: entry,
  }));

const objectValues = (value: unknown): TraceContextValue[] =>
  Object.entries(record(value) ?? {}).map(([label, entry]) => ({
    label,
    value: otlpValueText(entry),
  }));

const attributeMap = (value: unknown) =>
  new Map(
    otlpAttributes(value).map(({ label, value: entry }) => [label, entry])
  );

const spanAttributeMap = (value: unknown) => {
  const span = record(value);
  return new Map(
    [
      ...objectValues(span?.tags),
      ...objectValues(span?.data),
      ...otlpAttributes(value),
    ].map(({ label, value: entry }) => [label, entry])
  );
};

const first = (attributes: Map<string, string>, ...keys: string[]) => {
  for (const key of keys) {
    const value = attributes.get(key);
    if (value) {
      return value;
    }
  }
};

const compareBigInt = (left: bigint, right: bigint) => {
  if (left === right) {
    return 0;
  }
  return left < right ? -1 : 1;
};

const value = (
  attributes: Map<string, string>,
  label: string,
  ...keys: string[]
): TraceContextValue | undefined => {
  const found = first(attributes, ...keys);
  return found ? { label, value: found } : undefined;
};

const group = (
  label: string,
  values: (TraceContextValue | undefined)[]
): TraceContextGroup | undefined => {
  const present = values.filter(
    (entry): entry is TraceContextValue => entry !== undefined
  );
  return present.length > 0 ? { label, values: present } : undefined;
};

export const traceSemanticContext = (
  span: ServiceTraceSpan
): TraceContextGroup[] => {
  const attributes = spanAttributeMap(span.span);
  const resource = attributeMap(span.resource);
  return [
    group("HTTP", [
      value(attributes, "Method", "http.request.method", "http.method"),
      value(attributes, "Route", "http.route", "url.template"),
      value(attributes, "URL", "url.full", "http.url", "url.path"),
      value(
        attributes,
        "Status",
        "http.response.status_code",
        "http.status_code"
      ),
      value(attributes, "Server", "server.address", "net.peer.name"),
      value(attributes, "Protocol", "network.protocol.name", "http.flavor"),
      value(attributes, "User agent", "user_agent.original", "http.user_agent"),
    ]),
    group("Database", [
      value(attributes, "System", "db.system.name", "db.system"),
      value(attributes, "Namespace", "db.namespace", "db.name"),
      value(attributes, "Operation", "db.operation.name", "db.operation"),
      value(attributes, "Query", "db.query.summary", "db.statement"),
      value(attributes, "Collection", "db.collection.name"),
    ]),
    group("RPC", [
      value(attributes, "System", "rpc.system"),
      value(attributes, "Service", "rpc.service"),
      value(attributes, "Method", "rpc.method"),
      value(attributes, "Status", "rpc.grpc.status_code"),
    ]),
    group("Messaging", [
      value(attributes, "System", "messaging.system"),
      value(
        attributes,
        "Destination",
        "messaging.destination.name",
        "messaging.destination"
      ),
      value(
        attributes,
        "Operation",
        "messaging.operation.type",
        "messaging.operation"
      ),
      value(attributes, "Message ID", "messaging.message.id"),
    ]),
    group("Code", [
      value(attributes, "Function", "code.function.name", "code.function"),
      value(attributes, "Namespace", "code.namespace"),
      value(attributes, "File", "code.file.path", "code.filepath"),
      value(attributes, "Line", "code.line.number", "code.lineno"),
      value(attributes, "Thread", "thread.name", "thread.id"),
    ]),
    group("Runtime", [
      value(resource, "Service version", "service.version"),
      value(
        resource,
        "Environment",
        "deployment.environment.name",
        "deployment.environment"
      ),
      value(resource, "Instance", "service.instance.id"),
      value(resource, "Host", "host.name"),
      value(resource, "Container", "container.name", "container.id"),
      value(resource, "Runtime", "process.runtime.name"),
      value(resource, "Runtime version", "process.runtime.version"),
      value(resource, "Telemetry SDK", "telemetry.sdk.name"),
      value(resource, "Telemetry SDK version", "telemetry.sdk.version"),
    ]),
  ].filter((entry): entry is TraceContextGroup => entry !== undefined);
};

export const traceSpanEvents = (span: ServiceTraceSpan): TraceSpanEvent[] => {
  const events = record(span.span)?.events;
  if (!Array.isArray(events)) {
    return [];
  }
  return events.flatMap((item) => {
    const event = record(item);
    if (!event) {
      return [];
    }
    return [
      {
        attributes: otlpAttributes(event),
        name:
          typeof event.name === "string" && event.name
            ? event.name
            : "Span event",
        timeUnixNano:
          typeof event.timeUnixNano === "string"
            ? event.timeUnixNano
            : undefined,
      },
    ];
  });
};

export const traceSpanLinks = (span: ServiceTraceSpan): TraceSpanLink[] => {
  const links = record(span.span)?.links;
  if (!Array.isArray(links)) {
    return [];
  }
  return links.flatMap((item) => {
    const link = record(item);
    if (!link) {
      return [];
    }
    return [
      {
        attributes: otlpAttributes(link),
        spanID:
          typeof (link.spanId ?? link.span_id) === "string" &&
          (link.spanId ?? link.span_id)
            ? String(link.spanId ?? link.span_id)
            : undefined,
        traceID:
          typeof (link.traceId ?? link.trace_id) === "string" &&
          (link.traceId ?? link.trace_id)
            ? String(link.traceId ?? link.trace_id)
            : undefined,
      },
    ];
  });
};

const selfTimeFromChildren = (
  span: ServiceTraceSpan,
  children: ServiceTraceSpan[]
) => {
  const start = BigInt(span.startTimeUnixNano);
  const end = BigInt(span.endTimeUnixNano);
  const intervals = children
    .map((candidate) => ({
      end:
        BigInt(candidate.endTimeUnixNano) < end
          ? BigInt(candidate.endTimeUnixNano)
          : end,
      start:
        BigInt(candidate.startTimeUnixNano) > start
          ? BigInt(candidate.startTimeUnixNano)
          : start,
    }))
    .filter((interval) => interval.end > interval.start)
    .toSorted((left, right) => compareBigInt(left.start, right.start));
  let covered = 0n;
  let cursorStart: bigint | undefined;
  let cursorEnd: bigint | undefined;
  for (const interval of intervals) {
    if (cursorStart === undefined || cursorEnd === undefined) {
      cursorStart = interval.start;
      cursorEnd = interval.end;
      continue;
    }
    if (interval.start <= cursorEnd) {
      cursorEnd = interval.end > cursorEnd ? interval.end : cursorEnd;
      continue;
    }
    covered += cursorEnd - cursorStart;
    cursorStart = interval.start;
    cursorEnd = interval.end;
  }
  if (cursorStart !== undefined && cursorEnd !== undefined) {
    covered += cursorEnd - cursorStart;
  }
  const duration = end > start ? end - start : 0n;
  return duration > covered ? duration - covered : 0n;
};

export const traceSpanSelfTime = (
  span: ServiceTraceSpan,
  spans: ServiceTraceSpan[]
) =>
  selfTimeFromChildren(
    span,
    spans.filter((candidate) => candidate.parentSpanId === span.spanId)
  );

export const traceSpanSelfTimes = (spans: ServiceTraceSpan[]) => {
  const children = new Map<string, ServiceTraceSpan[]>();
  for (const span of spans) {
    if (!span.parentSpanId) {
      continue;
    }
    const siblings = children.get(span.parentSpanId);
    if (siblings) {
      siblings.push(span);
    } else {
      children.set(span.parentSpanId, [span]);
    }
  }
  return new Map(
    spans.map((span) => [
      span.spanId,
      selfTimeFromChildren(span, children.get(span.spanId) ?? []),
    ])
  );
};
