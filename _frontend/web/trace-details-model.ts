import type { ServiceTraceSpan } from "@/api";
import { asRecord } from "@/errors/event-context";
import { spanMatchesTraceQuery } from "@/trace-search";

export type WebVitalKey = "cls" | "fcp" | "inp" | "lcp" | "ttfb";
export type WebVitalStatus = "good" | "needs-improvement" | "poor";

export interface WebVitalMeasurement {
  key: WebVitalKey;
  name: string;
  status: WebVitalStatus;
  unit: string;
  value: number;
  valueMilliseconds?: number;
}

export interface TraceRow {
  childCount: number;
  depth: number;
  durationNano: string;
  endTimeUnixNano: string;
  groupedSpans: ServiceTraceSpan[];
  id: string;
  kind: "gap" | "group" | "span";
  label: string;
  span: ServiceTraceSpan;
  startTimeUnixNano: string;
}

export interface TraceRowsOptions {
  autoGroup?: boolean;
  expandedGroups?: ReadonlySet<string>;
  showGaps?: boolean;
}

type ChildTraceRow =
  | { end: bigint; kind: "gap"; start: bigint }
  | { kind: "group"; spans: ServiceTraceSpan[]; start: bigint }
  | { kind: "span"; span: ServiceTraceSpan; start: bigint };

const vitalDefinitions: Record<
  WebVitalKey,
  { good: number; median: number; name: string }
> = {
  cls: {
    good: 0.1,
    median: 0.25,
    name: "Cumulative Layout Shift",
  },
  fcp: {
    good: 900,
    median: 1600,
    name: "First Contentful Paint",
  },
  inp: {
    good: 200,
    median: 500,
    name: "Interaction to Next Paint",
  },
  lcp: {
    good: 1200,
    median: 2400,
    name: "Largest Contentful Paint",
  },
  ttfb: {
    good: 200,
    median: 400,
    name: "Time to First Byte",
  },
};

const vitalOrder: WebVitalKey[] = ["lcp", "fcp", "inp", "cls", "ttfb"];

const text = (value: unknown) =>
  typeof value === "string" && value !== "" ? value : undefined;

const number = (value: unknown) => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  }
};

const normalizedVitalKey = (value: string): WebVitalKey | undefined => {
  const normalized = value
    .toLowerCase()
    .replace(/^measurements\./u, "")
    .replace(/^ui\.webvital\./u, "")
    .replaceAll("_", "-");
  const aliases: Record<string, WebVitalKey> = {
    cls: "cls",
    "cumulative-layout-shift": "cls",
    fcp: "fcp",
    "first-contentful-paint": "fcp",
    inp: "inp",
    "interaction-to-next-paint": "inp",
    "largest-contentful-paint": "lcp",
    lcp: "lcp",
    "time-to-first-byte": "ttfb",
    ttfb: "ttfb",
  };
  return aliases[normalized];
};

const toMilliseconds = (value: number, unit: string) => {
  if (["second", "seconds", "s"].includes(unit)) {
    return value * 1000;
  }
  if (["nanosecond", "nanoseconds", "ns"].includes(unit)) {
    return value / 1_000_000;
  }
  if (["microsecond", "microseconds", "us", "μs"].includes(unit)) {
    return value / 1000;
  }
  return value;
};

const measurementStatus = (key: WebVitalKey, value: number) => {
  const definition = vitalDefinitions[key];
  if (value <= definition.good) {
    return "good" as const;
  }
  return value <= definition.median
    ? ("needs-improvement" as const)
    : ("poor" as const);
};

const measurement = (
  key: WebVitalKey,
  value: number,
  unit: string
): WebVitalMeasurement => {
  const normalizedUnit = key === "cls" ? "ratio" : unit || "millisecond";
  const comparable =
    key === "cls" ? value : toMilliseconds(value, normalizedUnit);
  return {
    key,
    name: vitalDefinitions[key].name,
    status: measurementStatus(key, comparable),
    unit: normalizedUnit,
    value,
    valueMilliseconds: key === "cls" ? undefined : comparable,
  };
};

const measurementsFromPayload = (payload: unknown) => {
  const result = new Map<WebVitalKey, WebVitalMeasurement>();
  const values = asRecord(asRecord(payload)?.measurements);
  for (const [rawKey, rawValue] of Object.entries(values ?? {})) {
    const key = normalizedVitalKey(rawKey);
    if (!key) {
      continue;
    }
    const record = asRecord(rawValue);
    const value = number(record?.value ?? rawValue);
    if (value === undefined) {
      continue;
    }
    result.set(
      key,
      measurement(key, value, text(record?.unit) ?? "millisecond")
    );
  }
  return result;
};

const standaloneVital = (
  span: ServiceTraceSpan
): WebVitalMeasurement | undefined => {
  const payload = asRecord(span.span);
  const operation = text(payload?.op) ?? "";
  const key = normalizedVitalKey(operation) ?? normalizedVitalKey(span.name);
  if (!key) {
    return;
  }
  const isWebVital =
    operation.includes("webvital") || operation === "web-vital";
  if (!isWebVital) {
    return;
  }
  const data = asRecord(payload?.data);
  const record = asRecord(payload?.measurement);
  const value = [record?.value, data?.value, payload?.value]
    .map(number)
    .find((candidate) => candidate !== undefined);
  if (value === undefined) {
    return measurementsFromPayload(payload).get(key);
  }
  const unit = [record?.unit, data?.unit, payload?.unit]
    .map(text)
    .find((candidate) => candidate !== undefined);
  return measurement(key, value, unit ?? "millisecond");
};

export const sentryTracePayload = (spans: ServiceTraceSpan[]) => {
  const root =
    spans.find((span) => span.source === "sentry" && span.isSegment) ??
    spans.find((span) => span.source === "sentry");
  return root ? asRecord(root.span) : undefined;
};

export const traceWebVitals = (
  spans: ServiceTraceSpan[]
): WebVitalMeasurement[] => {
  const values = measurementsFromPayload(sentryTracePayload(spans));
  for (const span of spans) {
    const standalone = standaloneVital(span);
    if (standalone) {
      values.set(standalone.key, standalone);
    }
  }
  return vitalOrder.flatMap((key) => {
    const value = values.get(key);
    return value ? [value] : [];
  });
};

export const formatWebVital = (vital: WebVitalMeasurement) => {
  if (vital.key === "cls") {
    return vital.value.toFixed(2);
  }
  const milliseconds = vital.valueMilliseconds ?? 0;
  if (milliseconds < 1000) {
    return `${Math.round(milliseconds)} ms`;
  }
  return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 2 : 1)} s`;
};

const chronological = (left: ServiceTraceSpan, right: ServiceTraceSpan) =>
  left.startTimeUnixNano.localeCompare(right.startTimeUnixNano, undefined, {
    numeric: true,
  });

const groupedChildren = (children: ServiceTraceSpan[], enabled: boolean) => {
  const first = new Map<string, ServiceTraceSpan[]>();
  const groupedIDs = new Set<string>();
  if (!enabled) {
    return { first, groupedIDs };
  }
  const candidates = new Map<string, ServiceTraceSpan[]>();
  for (const child of children) {
    const payload = asRecord(child.span);
    const key = [child.aiKind || payload?.op || child.name, child.kind].join(
      ":"
    );
    const values = candidates.get(key) ?? [];
    values.push(child);
    candidates.set(key, values);
  }
  for (const values of candidates.values()) {
    if (values.length < 5) {
      continue;
    }
    const [firstChild] = values;
    if (firstChild) {
      first.set(firstChild.spanId, values);
    }
    for (const child of values) {
      groupedIDs.add(child.spanId);
    }
  }
  return { first, groupedIDs };
};

const missingInstrumentationRows = (
  parent: ServiceTraceSpan,
  children: ServiceTraceSpan[]
): ChildTraceRow[] => {
  const gaps: ChildTraceRow[] = [];
  const parentEnd = BigInt(parent.endTimeUnixNano);
  let cursor = BigInt(parent.startTimeUnixNano);
  for (const child of children) {
    const childStart = BigInt(child.startTimeUnixNano);
    const childEnd = BigInt(child.endTimeUnixNano);
    if (childStart - cursor > 100_000_000n) {
      gaps.push({ end: childStart, kind: "gap", start: cursor });
    }
    if (childEnd > cursor) {
      cursor = childEnd;
    }
  }
  if (parentEnd - cursor > 100_000_000n) {
    gaps.push({ end: parentEnd, kind: "gap", start: cursor });
  }
  return gaps;
};

const traceChildRows = (
  parent: ServiceTraceSpan,
  children: ServiceTraceSpan[],
  groupingEnabled: boolean,
  showGaps: boolean
) => {
  const { first, groupedIDs } = groupedChildren(children, groupingEnabled);
  const rows: ChildTraceRow[] = [];
  for (const child of children) {
    const group = first.get(child.spanId);
    if (group) {
      rows.push({
        kind: "group",
        spans: group,
        start: BigInt(group[0]?.startTimeUnixNano ?? "0"),
      });
    } else if (!groupedIDs.has(child.spanId)) {
      rows.push({
        kind: "span",
        span: child,
        start: BigInt(child.startTimeUnixNano),
      });
    }
  }
  if (showGaps && children.length > 0) {
    rows.push(...missingInstrumentationRows(parent, children));
  }
  return rows.toSorted((left, right) => (left.start < right.start ? -1 : 1));
};

const repeatedGroupRow = (
  parent: ServiceTraceSpan,
  childRow: Extract<ChildTraceRow, { kind: "group" }>,
  depth: number
): TraceRow | undefined => {
  const [first] = childRow.spans;
  if (!first) {
    return;
  }
  let end = BigInt(first.endTimeUnixNano);
  for (const child of childRow.spans) {
    const childEnd = BigInt(child.endTimeUnixNano);
    if (childEnd > end) {
      end = childEnd;
    }
  }
  return {
    childCount: childRow.spans.length,
    depth: depth + 1,
    durationNano: (end - childRow.start).toString(),
    endTimeUnixNano: end.toString(),
    groupedSpans: childRow.spans,
    id: `group:${parent.spanId}:${first.spanId}`,
    kind: "group",
    label: `${childRow.spans.length.toLocaleString()} × ${first.name}`,
    span: first,
    startTimeUnixNano: childRow.start.toString(),
  };
};

export const traceRows = (
  spans: ServiceTraceSpan[],
  collapsed: ReadonlySet<string>,
  query = "",
  options: TraceRowsOptions = {}
): TraceRow[] => {
  const byID = new Map(spans.map((span) => [span.spanId, span]));
  const children = new Map<string, ServiceTraceSpan[]>();
  const roots: ServiceTraceSpan[] = [];
  for (const span of spans) {
    if (!span.parentSpanId || !byID.has(span.parentSpanId)) {
      roots.push(span);
    } else {
      const siblings = children.get(span.parentSpanId) ?? [];
      siblings.push(span);
      children.set(span.parentSpanId, siblings);
    }
  }
  roots.sort(chronological);
  for (const siblings of children.values()) {
    siblings.sort(chronological);
  }

  const matches = (
    span: ServiceTraceSpan,
    visited = new Set<string>()
  ): boolean => {
    if (visited.has(span.spanId)) {
      return false;
    }
    visited.add(span.spanId);
    return (
      spanMatchesTraceQuery(span, spans, query) ||
      (children.get(span.spanId) ?? []).some((child) => matches(child, visited))
    );
  };
  const rows: TraceRow[] = [];
  const connected = new Set<string>();
  const markConnected = (span: ServiceTraceSpan) => {
    if (connected.has(span.spanId)) {
      return;
    }
    connected.add(span.spanId);
    for (const child of children.get(span.spanId) ?? []) {
      markConnected(child);
    }
  };
  for (const root of roots) {
    markConnected(root);
  }
  const append = (
    span: ServiceTraceSpan,
    depth: number,
    visited: Set<string>
  ) => {
    if (visited.has(span.spanId) || (query.trim() && !matches(span))) {
      return;
    }
    visited.add(span.spanId);
    const spanChildren = children.get(span.spanId) ?? [];
    rows.push({
      childCount: spanChildren.length,
      depth,
      durationNano: span.durationNano,
      endTimeUnixNano: span.endTimeUnixNano,
      groupedSpans: [],
      id: span.spanId,
      kind: "span",
      label: span.name,
      span,
      startTimeUnixNano: span.startTimeUnixNano,
    });
    if (!query.trim() && collapsed.has(span.spanId)) {
      return;
    }

    const childRows = traceChildRows(
      span,
      spanChildren,
      options.autoGroup === true && !query.trim(),
      options.showGaps === true && !query.trim()
    );

    for (const childRow of childRows) {
      if (childRow.kind === "span") {
        append(childRow.span, depth + 1, visited);
        continue;
      }
      if (childRow.kind === "gap") {
        const duration = childRow.end - childRow.start;
        rows.push({
          childCount: 0,
          depth: depth + 1,
          durationNano: duration.toString(),
          endTimeUnixNano: childRow.end.toString(),
          groupedSpans: [],
          id: `gap:${span.spanId}:${childRow.start.toString()}`,
          kind: "gap",
          label: "Missing instrumentation",
          span,
          startTimeUnixNano: childRow.start.toString(),
        });
        continue;
      }
      const groupRow = repeatedGroupRow(span, childRow, depth);
      if (!groupRow) {
        continue;
      }
      rows.push(groupRow);
      if (options.expandedGroups?.has(groupRow.id)) {
        for (const child of childRow.spans) {
          append(child, depth + 2, visited);
        }
      } else {
        for (const child of childRow.spans) {
          visited.add(child.spanId);
        }
      }
    }
  };
  const visited = new Set<string>();
  for (const root of roots) {
    append(root, 0, visited);
  }
  // Cyclic parent references should not make valid spans disappear from the trace.
  for (const span of spans) {
    if (!connected.has(span.spanId)) {
      append(span, 0, visited);
    }
  }
  return rows;
};

export const webVitalKeys = vitalOrder;

export const webVitalName = (key: WebVitalKey) => vitalDefinitions[key].name;
