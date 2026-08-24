import { aiTool } from "@/ai-trace";
import type { ServiceTraceSpan, ServiceTraceWebVital } from "@/api";
import { asRecord } from "@/errors/event-context";
import { matchingTraceSpans } from "@/trace-search";
import type { ServiceNameResolver } from "@/trace-service-name";

export type WebVitalKey = "cls" | "fcp" | "inp" | "lcp" | "ttfb";
export type WebVitalStatus = "good" | "needs-improvement" | "poor";

export interface WebVitalMeasurement {
  key: WebVitalKey;
  name: string;
  spanId: string;
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
  matchingSpanIDs?: ReadonlySet<string>;
  serviceName?: ServiceNameResolver;
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
    good: 1800,
    median: 3000,
    name: "First Contentful Paint",
  },
  inp: {
    good: 200,
    median: 500,
    name: "Interaction to Next Paint",
  },
  lcp: {
    good: 2500,
    median: 4000,
    name: "Largest Contentful Paint",
  },
  ttfb: {
    good: 800,
    median: 1800,
    name: "Time to First Byte",
  },
};

const vitalOrder: WebVitalKey[] = ["lcp", "fcp", "inp", "cls", "ttfb"];
const otelContextIsRemoteMask = 512;

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
  unit: string,
  spanId = "",
  status = measurementStatus(
    key,
    key === "cls" ? value : toMilliseconds(value, unit)
  )
): WebVitalMeasurement => {
  const normalizedUnit = key === "cls" ? "ratio" : unit || "millisecond";
  const comparable =
    key === "cls" ? value : toMilliseconds(value, normalizedUnit);
  return {
    key,
    name: vitalDefinitions[key].name,
    spanId,
    status,
    unit: normalizedUnit,
    value,
    valueMilliseconds: key === "cls" ? undefined : comparable,
  };
};

export const traceRoot = (spans: ServiceTraceSpan[]) =>
  spans.find((span) => !span.parentSpanId) ??
  spans.find(
    (span) => Math.floor(span.flags / otelContextIsRemoteMask) % 2 === 1
  ) ??
  spans[0];

export const traceWebVitals = (
  webVitals: ServiceTraceWebVital[]
): WebVitalMeasurement[] => {
  const values = new Map<WebVitalKey, WebVitalMeasurement>();
  for (const vital of webVitals) {
    const key = normalizedVitalKey(vital.name);
    if (key) {
      values.set(
        key,
        measurement(
          key,
          vital.value,
          key === "cls" ? "ratio" : "millisecond",
          vital.spanId,
          vital.rating === "good" ||
            vital.rating === "needs-improvement" ||
            vital.rating === "poor"
            ? vital.rating
            : undefined
        )
      );
    }
  }
  return vitalOrder.flatMap((key) => {
    const value = values.get(key);
    return value ? [value] : [];
  });
};

export interface WebVitalTimelineMarker {
  timestampUnixNano: bigint;
  vital: WebVitalMeasurement;
}

export const webVitalTimelineMarkers = (
  vitals: WebVitalMeasurement[],
  spans: ServiceTraceSpan[],
  fallbackStart: bigint
): WebVitalTimelineMarker[] => {
  const spanStarts = new Map(
    spans.map((span) => [span.spanId, BigInt(span.startTimeUnixNano)] as const)
  );
  return vitals.flatMap((vital) => {
    if (
      !(vital.key === "ttfb" || vital.key === "fcp" || vital.key === "lcp") ||
      vital.valueMilliseconds === undefined
    ) {
      return [];
    }
    const anchor = spanStarts.get(vital.spanId) ?? fallbackStart;
    return [
      {
        timestampUnixNano:
          anchor + BigInt(Math.round(vital.valueMilliseconds * 1_000_000)),
        vital,
      },
    ];
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
    const key = [
      child.aiKind,
      child.aiKind === "tool" ? aiTool(child).name : "",
      child.aiProvider,
      child.aiModel,
      child.aiAgent,
      child.aiOperation,
      payload?.op ?? "",
      child.name,
      child.kind,
    ].join("\u0000");
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
  return rows.toSorted((left, right) => {
    if (left.start === right.start) {
      return 0;
    }
    return left.start < right.start ? -1 : 1;
  });
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

const traceHierarchy = (spans: ServiceTraceSpan[]) => {
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
  return { byID, children, roots };
};

export const collapsibleTraceSpanIDs = (spans: ServiceTraceSpan[]) => {
  const spanIDs = new Set(spans.map((span) => span.spanId));
  const collapsible = new Set<string>();
  for (const span of spans) {
    if (spanIDs.has(span.parentSpanId)) {
      collapsible.add(span.parentSpanId);
    }
  }
  return collapsible;
};

const visibleTraceSpanIDs = (
  spans: ServiceTraceSpan[],
  byID: ReadonlyMap<string, ServiceTraceSpan>,
  query: string,
  matchingSpanIDs?: ReadonlySet<string>,
  serviceName?: ServiceNameResolver
) => {
  const visible = new Set<string>();
  if (!query) {
    return visible;
  }
  const directMatches =
    matchingSpanIDs ??
    new Set(
      matchingTraceSpans(spans, query, serviceName).map((span) => span.spanId)
    );
  for (const span of spans) {
    if (!directMatches.has(span.spanId)) {
      continue;
    }
    let current: ServiceTraceSpan | undefined = span;
    const path = new Set<string>();
    while (current && !path.has(current.spanId)) {
      path.add(current.spanId);
      if (visible.has(current.spanId)) {
        break;
      }
      visible.add(current.spanId);
      current = byID.get(current.parentSpanId);
    }
  }
  return visible;
};

const connectedTraceSpanIDs = (
  roots: ServiceTraceSpan[],
  children: ReadonlyMap<string, ServiceTraceSpan[]>
) => {
  const connected = new Set<string>();
  const pending = [...roots];
  while (pending.length > 0) {
    const span = pending.pop();
    if (!(span && !connected.has(span.spanId))) {
      continue;
    }
    connected.add(span.spanId);
    pending.push(...(children.get(span.spanId) ?? []));
  }
  return connected;
};

type AppendOperation =
  | { depth: number; span: ServiceTraceSpan; type: "visit" }
  | { row: TraceRow; type: "emit" }
  | { spanID: string; type: "mark" };

const spanTraceRow = (
  span: ServiceTraceSpan,
  childCount: number,
  depth: number
): TraceRow => ({
  childCount,
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

const childAppendOperations = (
  parent: ServiceTraceSpan,
  childRow: ChildTraceRow,
  depth: number,
  expandedGroups?: ReadonlySet<string>
): AppendOperation[] => {
  if (childRow.kind === "span") {
    return [{ depth: depth + 1, span: childRow.span, type: "visit" }];
  }
  if (childRow.kind === "gap") {
    return [
      {
        row: {
          childCount: 0,
          depth: depth + 1,
          durationNano: (childRow.end - childRow.start).toString(),
          endTimeUnixNano: childRow.end.toString(),
          groupedSpans: [],
          id: `gap:${parent.spanId}:${childRow.start.toString()}`,
          kind: "gap",
          label: "Missing instrumentation",
          span: parent,
          startTimeUnixNano: childRow.start.toString(),
        },
        type: "emit",
      },
    ];
  }
  const groupRow = repeatedGroupRow(parent, childRow, depth);
  if (!groupRow) {
    return [];
  }
  const children = childRow.spans.toReversed();
  const operations: AppendOperation[] = expandedGroups?.has(groupRow.id)
    ? children.map((span) => ({ depth: depth + 2, span, type: "visit" }))
    : children.map((span) => ({ spanID: span.spanId, type: "mark" }));
  operations.push({ row: groupRow, type: "emit" });
  return operations;
};

interface AppendTraceRowsOptions extends TraceRowsOptions {
  children: ReadonlyMap<string, ServiceTraceSpan[]>;
  collapsed: ReadonlySet<string>;
  normalizedQuery: string;
  rows: TraceRow[];
  visible: ReadonlySet<string>;
  visited: Set<string>;
}

const appendTraceRows = (
  span: ServiceTraceSpan,
  depth: number,
  options: AppendTraceRowsOptions
) => {
  const operations: AppendOperation[] = [{ depth, span, type: "visit" }];
  while (operations.length > 0) {
    const operation = operations.pop();
    if (!operation) {
      continue;
    }
    if (operation.type === "emit") {
      options.rows.push(operation.row);
      continue;
    }
    if (operation.type === "mark") {
      options.visited.add(operation.spanID);
      continue;
    }
    if (
      options.visited.has(operation.span.spanId) ||
      (options.normalizedQuery && !options.visible.has(operation.span.spanId))
    ) {
      continue;
    }
    options.visited.add(operation.span.spanId);
    const spanChildren = options.children.get(operation.span.spanId) ?? [];
    options.rows.push(
      spanTraceRow(operation.span, spanChildren.length, operation.depth)
    );
    if (
      !options.normalizedQuery &&
      options.collapsed.has(operation.span.spanId)
    ) {
      continue;
    }
    const childRows = traceChildRows(
      operation.span,
      spanChildren,
      options.autoGroup === true && !options.normalizedQuery,
      options.showGaps === true && !options.normalizedQuery
    );
    for (const childRow of childRows.toReversed()) {
      operations.push(
        ...childAppendOperations(
          operation.span,
          childRow,
          operation.depth,
          options.expandedGroups
        )
      );
    }
  }
};

export const traceRows = (
  spans: ServiceTraceSpan[],
  collapsed: ReadonlySet<string>,
  query = "",
  options: TraceRowsOptions = {}
): TraceRow[] => {
  const { byID, children, roots } = traceHierarchy(spans);
  const normalizedQuery = query.trim();
  const visible = visibleTraceSpanIDs(
    spans,
    byID,
    normalizedQuery,
    options.matchingSpanIDs,
    options.serviceName
  );
  const connected = connectedTraceSpanIDs(roots, children);
  const rows: TraceRow[] = [];
  const appendOptions: AppendTraceRowsOptions = {
    ...options,
    children,
    collapsed,
    normalizedQuery,
    rows,
    visible,
    visited: new Set(),
  };
  for (const root of roots) {
    appendTraceRows(root, 0, appendOptions);
  }
  // Cyclic parent references should not make valid spans disappear from the trace.
  for (const span of spans) {
    if (!connected.has(span.spanId)) {
      appendTraceRows(span, 0, appendOptions);
    }
  }
  return rows;
};

export const webVitalKeys = vitalOrder;

export const webVitalName = (key: WebVitalKey) => vitalDefinitions[key].name;
