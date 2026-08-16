import type { ServiceTraceSpan } from "@/api";
import { asRecord } from "@/errors/event-context";
import { traceSpanSelfTime } from "@/trace-span-context";

interface SearchToken {
  field?: string;
  value: string;
}

const tokenPattern = /(?:[^\s"]+|"[^"]*")+/gu;
const comparisonPattern =
  /^(?<operator><=|>=|=|<|>)?\s*(?<amount>\d+(?:\.\d+)?)\s*(?<unit>ns|us|µs|ms|s|m|h)?$/iu;

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

const attributeValue = (value: unknown, key: string) => {
  const attributes = asRecord(value)?.attributes;
  if (!Array.isArray(attributes)) {
    return "";
  }
  for (const attribute of attributes) {
    const record = asRecord(attribute);
    if (record?.key !== key) {
      continue;
    }
    const wrapped = asRecord(record.value);
    const candidate = wrapped ? Object.values(wrapped)[0] : undefined;
    return candidate === undefined ? "" : String(candidate);
  }
  return "";
};

const spanOperation = (span: ServiceTraceSpan) => {
  const payload = asRecord(span.span);
  return String(
    payload?.op ??
      attributeValue(span.span, "gen_ai.operation.name") ??
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
  if (value === "profile") {
    return Boolean(payload?.profile_id ?? payload?.profileId);
  }
  if (value === "link") {
    return Array.isArray(payload?.links) && payload.links.length > 0;
  }
  return false;
};

const searchable = (span: ServiceTraceSpan) =>
  normalize(
    [
      span.name,
      span.serviceId,
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
  allSpans: ServiceTraceSpan[],
  token: SearchToken
) => {
  if (!token.field) {
    return searchable(span).includes(token.value);
  }
  if (["duration", "duration_ms"].includes(token.field)) {
    return compare(Number(span.durationNano), token.value);
  }
  if (["self", "self_time"].includes(token.field)) {
    return compare(Number(traceSpanSelfTime(span, allSpans)), token.value);
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
    return normalize(
      attributeValue(span.resource, "service.name") || span.serviceId
    ).includes(token.value);
  }
  if (token.field === "name") {
    return normalize(span.name).includes(token.value);
  }
  if (token.field === "has") {
    return hasFeature(span, token.value);
  }
  return searchable(span).includes(`${token.field}:${token.value}`);
};

export const spanMatchesTraceQuery = (
  span: ServiceTraceSpan,
  allSpans: ServiceTraceSpan[],
  query: string
) => tokens(query).every((token) => matchesToken(span, allSpans, token));

export const matchingTraceSpans = (spans: ServiceTraceSpan[], query: string) =>
  query.trim()
    ? spans.filter((span) => spanMatchesTraceQuery(span, spans, query))
    : spans;
