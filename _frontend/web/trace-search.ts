import type { ServiceTraceSpan } from "@/api";
import { asRecord } from "@/errors/event-context";
import { otlpAttribute } from "@/otlp";
import { traceServiceName } from "@/trace-service-name";
import type { ServiceNameResolver } from "@/trace-service-name";
import { traceSpanSelfTimes } from "@/trace-span-context";

interface SearchToken {
  field?: string;
  value: string;
}

const tokenPattern = /(?:[^\s"]+|"[^"]*")+/gu;
const comparisonPattern =
  /^(?<operator><=|>=|=|<|>)?\s*(?<amount>\d+(?:\.\d+)?)\s*(?<unit>ns|us|µs|ms|s|m|h)?$/iu;
const structuredSearchFields = new Set([
  "duration",
  "duration_ms",
  "has",
  "name",
  "op",
  "operation",
  "self",
  "self_time",
  "service",
  "source",
  "status",
]);

const normalize = (value: string) => value.trim().toLocaleLowerCase();

const tokens = (query: string): SearchToken[] =>
  (query.match(tokenPattern) ?? []).flatMap((raw) => {
    const separator = raw.indexOf(":");
    const field =
      separator > 0 ? normalize(raw.slice(0, separator)) : undefined;
    const rawValue = separator > 0 ? raw.slice(separator + 1) : raw;
    const value = normalize(rawValue.replaceAll(/^"|"$/gu, ""));
    return value ? [{ field, value }] : [];
  });

const durationNanos = (value: string) => {
  const match = comparisonPattern.exec(value);
  if (!match) {
    return;
  }
  const amount = Number(match.groups?.amount);
  const multipliers = {
    h: 3_600_000_000_000,
    m: 60_000_000_000,
    ms: 1_000_000,
    ns: 1,
    s: 1_000_000_000,
    us: 1000,
    µs: 1000,
  } as const;
  const unit = (match.groups?.unit?.toLocaleLowerCase() ??
    "ms") as keyof typeof multipliers;
  const multiplier = multipliers[unit] ?? multipliers.ms;
  return {
    nanos: amount * multiplier,
    operator: match.groups?.operator ?? "=",
  };
};

const compare = (actual: number, expression: string) => {
  const parsed = durationNanos(expression);
  if (!parsed) {
    return false;
  }
  if (parsed.operator === ">") {
    return actual > parsed.nanos;
  }
  if (parsed.operator === ">=") {
    return actual >= parsed.nanos;
  }
  if (parsed.operator === "<") {
    return actual < parsed.nanos;
  }
  if (parsed.operator === "<=") {
    return actual <= parsed.nanos;
  }
  return actual === parsed.nanos;
};

const spanOperation = (span: ServiceTraceSpan) => {
  const payload = asRecord(span.span);
  return String(
    payload?.op ??
      otlpAttribute(span.span, "gen_ai.operation.name") ??
      span.aiOperation ??
      ""
  );
};

const spanStatus = (span: ServiceTraceSpan) => {
  if (span.statusCode === 2) {
    return "error";
  }
  if (span.statusCode === 1) {
    return "ok";
  }
  return span.statusMessage || "unset";
};

const hasFeature = (span: ServiceTraceSpan, value: string) => {
  const payload = asRecord(span.span);
  if (value === "error") {
    return span.statusCode === 2;
  }
  if (value === "issue") {
    return span.source === "sentry_error" || Boolean(payload?.issue_id);
  }
  if (value === "link") {
    return Array.isArray(payload?.links) && payload.links.length > 0;
  }
  return false;
};

const searchable = (
  span: ServiceTraceSpan,
  serviceName?: ServiceNameResolver
) =>
  normalize(
    [
      span.name,
      span.serviceId,
      traceServiceName(span, serviceName),
      span.source,
      span.aiAgent,
      span.aiKind,
      span.aiModel,
      span.aiOperation,
      span.aiProvider,
      spanStatus(span),
      spanOperation(span),
      JSON.stringify(span.resource),
      JSON.stringify(span.span),
    ].join(" ")
  );

const matchesToken = (
  span: ServiceTraceSpan,
  token: SearchToken,
  selfTimes: ReadonlyMap<string, bigint>,
  searchableText: string,
  serviceName?: ServiceNameResolver
) => {
  if (!token.field) {
    return searchableText.includes(token.value);
  }
  if (["duration", "duration_ms"].includes(token.field)) {
    return compare(Number(span.durationNano), token.value);
  }
  if (["self", "self_time"].includes(token.field)) {
    return compare(Number(selfTimes.get(span.spanId) ?? 0n), token.value);
  }
  if (token.field === "status") {
    return normalize(spanStatus(span)) === token.value;
  }
  if (token.field === "source") {
    return normalize(span.source).includes(token.value);
  }
  if (["op", "operation"].includes(token.field)) {
    return normalize(spanOperation(span)).includes(token.value);
  }
  if (token.field === "service") {
    return normalize(traceServiceName(span, serviceName)).includes(token.value);
  }
  if (token.field === "name") {
    return normalize(span.name).includes(token.value);
  }
  if (token.field === "has") {
    return hasFeature(span, token.value);
  }
  return searchableText.includes(`${token.field}:${token.value}`);
};

export const matchingTraceSpans = (
  spans: ServiceTraceSpan[],
  query: string,
  serviceName?: ServiceNameResolver
) => {
  const queryTokens = tokens(query);
  if (queryTokens.length === 0) {
    return spans;
  }
  const needsSelfTime = queryTokens.some((token) =>
    ["self", "self_time"].includes(token.field ?? "")
  );
  const needsSearchableText = queryTokens.some(
    (token) => !token.field || !structuredSearchFields.has(token.field)
  );
  const selfTimes = needsSelfTime ? traceSpanSelfTimes(spans) : new Map();
  return spans.filter((span) => {
    const searchableText = needsSearchableText
      ? searchable(span, serviceName)
      : "";
    return queryTokens.every((token) =>
      matchesToken(span, token, selfTimes, searchableText, serviceName)
    );
  });
};
