import {
  ArrowLeft,
  Bot,
  BrainCircuit,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  CircleAlert,
  CircleDashed,
  ChevronsDownUp,
  ChevronsUpDown,
  Diamond,
  Gauge,
  Link2,
  Logs,
  RotateCcw,
  Search,
  Wrench,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";

import { AiSpanDetails } from "@/ai-span-details";
import { aiSpanLabel } from "@/ai-trace";
import { fetchServiceReplayRecording } from "@/api";
import type {
  MetricScope,
  ServiceTraceDetail,
  ServiceTraceMetricSample,
  ServiceTraceSpan,
} from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { asRecord, eventTags, recordRows } from "@/errors/event-context";
import {
  EventEnvironmentSection,
  EventRequestSection,
} from "@/errors/event-context-sections";
import {
  BreadcrumbsSection,
  ContextSection,
  EventMessageSection,
} from "@/errors/event-sections";
import { RelatedReplay } from "@/errors/related-replay";
import { cn } from "@/lib/utils";
import {
  formatWebVital,
  sentryTracePayload,
  traceRows,
  traceWebVitals,
  webVitalKeys,
  webVitalName,
} from "@/trace-details-model";
import type { TraceRow, WebVitalMeasurement } from "@/trace-details-model";
import { TraceProfileFlamegraph } from "@/trace-profile-flamegraph";
import { TraceRelatedLogs } from "@/trace-related-logs";
import { matchingTraceSpans } from "@/trace-search";
import { traceSpanSelfTime } from "@/trace-span-context";
import { TraceSpanContext } from "@/trace-span-context-view";
import { buildTraceTimeline, traceViewport } from "@/trace-timeline";
import type { TraceViewport } from "@/trace-timeline";

const integer = (value: string) => globalThis.BigInt(value);

const nanosToDate = (value: string) =>
  new Date(Number(integer(value) / 1_000_000n));

const nanosToMilliseconds = (value: string) =>
  Number(integer(value)) / 1_000_000;

const formatDuration = (value: string) => {
  const milliseconds = nanosToMilliseconds(value);
  if (milliseconds < 1) {
    return `${Math.round(milliseconds * 1000)} μs`;
  }
  if (milliseconds < 1000) {
    return `${milliseconds.toFixed(milliseconds < 10 ? 2 : 1)} ms`;
  }
  return `${(milliseconds / 1000).toFixed(2)} s`;
};

const formatDurationNanos = (value: number) =>
  formatDuration(Math.max(0, Math.round(value)).toString());

const otlpValue = (value: unknown): string => {
  if (value === null || value === undefined) {
    return "null";
  }
  if (typeof value !== "object") {
    return String(value);
  }
  const record = value as Record<string, unknown>;
  for (const candidate of [
    "stringValue",
    "intValue",
    "doubleValue",
    "boolValue",
    "bytesValue",
  ]) {
    if (record[candidate] !== undefined) {
      return String(record[candidate]);
    }
  }
  if ("value" in record) {
    return otlpValue(record.value);
  }
  return JSON.stringify(value);
};

const otlpAttributes = (value: unknown) => {
  const attributes = asRecord(value)?.attributes;
  const object = asRecord(attributes);
  if (object) {
    return Object.entries(object).map(([key, entry]) => ({
      key,
      value: otlpValue(entry),
    }));
  }
  if (!Array.isArray(attributes)) {
    return [];
  }
  return attributes.flatMap((item) => {
    const record = asRecord(item);
    return record && typeof record.key === "string"
      ? [{ key: record.key, value: otlpValue(record.value) }]
      : [];
  });
};

const otlpAttribute = (value: unknown, key: string) =>
  otlpAttributes(value).find((item) => item.key === key)?.value;

const text = (value: unknown) =>
  typeof value === "string" && value !== "" ? value : undefined;

const spanKind = (kind: number) =>
  ({
    1: "internal",
    2: "server",
    3: "client",
    4: "producer",
    5: "consumer",
  })[kind] ?? "unspecified";

const spanStatus = (span: ServiceTraceSpan) => {
  if (span.statusCode === 2) {
    return "error";
  }
  if (span.statusCode === 1) {
    return "ok";
  }
  return span.statusMessage || "unset";
};

const replayID = (payload: unknown) => {
  const event = asRecord(payload);
  const contexts = asRecord(event?.contexts);
  const replay = asRecord(contexts?.replay);
  const direct = text(event?.replay_id) ?? text(replay?.replay_id);
  if (direct) {
    return direct;
  }
  return eventTags(payload).find(([key]) =>
    ["replayid", "replay_id"].includes(key.toLowerCase())
  )?.[1];
};

const traceBounds = (spans: ServiceTraceSpan[]) => {
  let start = integer(spans[0]?.startTimeUnixNano ?? "0");
  let end = start;
  for (const span of spans) {
    const spanStart = integer(span.startTimeUnixNano);
    const spanEnd = integer(span.endTimeUnixNano);
    start = spanStart < start ? spanStart : start;
    end = spanEnd > end ? spanEnd : end;
  }
  return { duration: end > start ? end - start : 1n, end, start };
};

const SpanMarker = ({ span }: { span: ServiceTraceSpan }) => {
  if (span.source === "sentry_error") {
    return <CircleAlert className="size-3 shrink-0 text-destructive" />;
  }
  if (span.statusCode === 2) {
    return <CircleAlert className="size-3 shrink-0 text-destructive" />;
  }
  if (span.aiKind) {
    if (span.aiKind === "tool") {
      return <Wrench className="size-3 shrink-0 text-violet-500" />;
    }
    if (["model", "embedding", "rerank"].includes(span.aiKind)) {
      return <BrainCircuit className="size-3 shrink-0 text-violet-500" />;
    }
    return <Bot className="size-3 shrink-0 text-violet-500" />;
  }
  const operation = text(asRecord(span.span)?.op) ?? "";
  if (
    integer(span.durationNano) === 0n ||
    operation === "mark" ||
    operation === "paint" ||
    operation.includes("webvital")
  ) {
    return <Diamond className="size-2.5 shrink-0 fill-sky-500 text-sky-500" />;
  }
  return <span className="size-1.5 shrink-0 bg-sky-500" />;
};

const spanBarColor = (span: ServiceTraceSpan) => {
  if (span.statusCode === 2) {
    return "bg-destructive";
  }
  return span.aiKind ? "bg-violet-500" : "bg-sky-500";
};

const errorMarker = (span: ServiceTraceSpan) => {
  if (span.source !== "sentry_error") {
    return;
  }
  const payload = asRecord(span.span);
  const issueID = text(payload?.issue_id);
  const eventID = text(payload?.event_id);
  return issueID ? { eventID, issueID } : undefined;
};

const linkedTraces = (span: ServiceTraceSpan) => {
  const links = asRecord(span.span)?.links;
  if (!Array.isArray(links)) {
    return [];
  }
  return links.flatMap((link) => {
    const value = asRecord(link);
    const traceID = text(value?.traceId) ?? text(value?.trace_id);
    const spanID = text(value?.spanId) ?? text(value?.span_id);
    return traceID ? [{ spanID, traceID }] : [];
  });
};

const rowValues = (value: unknown) =>
  recordRows(value).map(([key, rowValue]) => ({ key, value: rowValue }));

const traceAttributeGroups = (span: ServiceTraceSpan) => {
  if (span.aiKind) {
    return [];
  }
  const payload = asRecord(span.span);
  return [
    { label: "Span attributes", values: otlpAttributes(span.span) },
    { label: "Span data", values: rowValues(payload?.data) },
    { label: "Measurements", values: rowValues(payload?.measurements) },
    { label: "Resource", values: otlpAttributes(span.resource) },
    { label: "Instrumentation", values: otlpAttributes(span.scope) },
  ].filter((group) => group.values.length > 0);
};

const TraceAttributeHeader = ({
  span,
  traceStart,
}: {
  span: ServiceTraceSpan;
  traceStart: bigint;
}) => {
  const offset = integer(span.startTimeUnixNano) - traceStart;
  return (
    <header className="px-4 py-3">
      <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
        {span.aiKind || "Selected span"}
      </p>
      <h3 className="mt-1 text-xs font-medium">
        {span.aiKind ? aiSpanLabel(span) : span.name}
      </h3>
      <p className="mt-1 text-[9px] text-muted-foreground">
        {spanKind(span.kind)} · {formatDuration(span.durationNano)} · +
        {formatDuration(offset.toString())}
      </p>
    </header>
  );
};

const TraceSpanActions = ({
  marker,
  onOpenError,
  onOpenLogs,
  span,
}: {
  marker?: ReturnType<typeof errorMarker>;
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  span: ServiceTraceSpan;
}) => (
  <>
    {onOpenLogs ? (
      <div className="flex items-center border-t border-border px-3 py-2">
        <Button
          onClick={() => onOpenLogs(span.traceId, span.spanId)}
          size="sm"
          variant="ghost"
        >
          <Logs /> View span logs
        </Button>
      </div>
    ) : null}
    {marker && onOpenError ? (
      <div className="flex items-center border-t border-border px-3 py-2">
        <Button
          onClick={() => onOpenError(marker.issueID, marker.eventID)}
          size="sm"
          variant="ghost"
        >
          <CircleAlert /> Open issue
        </Button>
      </div>
    ) : null}
  </>
);

const TraceSpanMetadata = ({
  onSelectSpan,
  operation,
  scope,
  service,
  span,
  traceSpans,
}: {
  onSelectSpan: (spanID: string) => void;
  operation?: string;
  scope?: Record<string, unknown>;
  service?: string;
  span: ServiceTraceSpan;
  traceSpans: ServiceTraceSpan[];
}) => {
  const childCount = traceSpans.filter(
    (candidate) => candidate.parentSpanId === span.spanId
  ).length;
  const selfTime = traceSpanSelfTime(span, traceSpans);
  const baseline = span.baselineDurationNano;
  const baselineDelta =
    baseline && baseline > 0
      ? (Number(span.durationNano) - baseline) / baseline
      : undefined;
  const showBaseline =
    baseline !== null &&
    baseline !== undefined &&
    baselineDelta !== undefined &&
    Math.abs(baselineDelta) >= 0.1;
  return (
    <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-y-2 border-t border-border px-4 py-3 text-[9px]">
      <dt className="text-muted-foreground">Status</dt>
      <dd className={span.statusCode === 2 ? "text-destructive" : ""}>
        {spanStatus(span)}
      </dd>
      <dt className="text-muted-foreground">Started</dt>
      <dd>{nanosToDate(span.startTimeUnixNano).toLocaleString()}</dd>
      <dt className="text-muted-foreground">Service</dt>
      <dd>{service ?? "Current service"}</dd>
      <dt className="text-muted-foreground">Operation</dt>
      <dd>{operation ?? spanKind(span.kind)}</dd>
      <dt className="text-muted-foreground">Self time</dt>
      <dd>{formatDuration(selfTime.toString())}</dd>
      {showBaseline && baselineDelta !== undefined ? (
        <>
          <dt className="text-muted-foreground">24h baseline</dt>
          <dd
            className={
              baselineDelta > 0 ? "text-amber-500" : "text-emerald-500"
            }
          >
            {Math.abs(baselineDelta * 100).toFixed(0)}%{" "}
            {baselineDelta > 0 ? "slower" : "faster"}
            <span className="ml-1 text-muted-foreground">
              · avg {formatDurationNanos(baseline)}
            </span>
          </dd>
        </>
      ) : null}
      <dt className="text-muted-foreground">Children</dt>
      <dd>{childCount.toLocaleString()}</dd>
      <dt className="text-muted-foreground">Instrumentation</dt>
      <dd>
        {text(scope?.name) ?? "—"}
        {text(scope?.version) ? ` ${String(scope?.version)}` : ""}
      </dd>
      <dt className="text-muted-foreground">Span ID</dt>
      <dd className="overflow-x-auto font-mono">{span.spanId}</dd>
      <dt className="text-muted-foreground">Parent span</dt>
      <dd className="min-w-0 overflow-x-auto font-mono">
        {span.parentSpanId ? (
          <button
            className="text-sky-600 hover:underline dark:text-sky-400"
            onClick={() => onSelectSpan(span.parentSpanId)}
            type="button"
          >
            {span.parentSpanId}
          </button>
        ) : (
          "root"
        )}
      </dd>
    </dl>
  );
};

const TraceLinks = ({
  links,
  onOpenTrace,
}: {
  links: ReturnType<typeof linkedTraces>;
  onOpenTrace?: (traceID: string, segmentID?: string) => void;
}) =>
  links.length > 0 ? (
    <div className="border-t border-border px-4 py-3">
      <p className="mb-2 flex items-center gap-1.5 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
        <Link2 className="size-3" /> Linked traces
      </p>
      <div className="space-y-1">
        {links.map((link) => (
          <button
            className="block w-full truncate text-left font-mono text-[9px] text-sky-600 hover:underline dark:text-sky-400"
            disabled={!onOpenTrace}
            key={`${link.traceID}:${link.spanID ?? ""}`}
            onClick={() => onOpenTrace?.(link.traceID)}
            title={link.traceID}
            type="button"
          >
            {link.traceID}
            {link.spanID ? ` · ${link.spanID}` : ""}
          </button>
        ))}
      </div>
    </div>
  ) : null;

const TraceMetricExemplars = ({
  metrics,
}: {
  metrics: ServiceTraceMetricSample[];
}) =>
  metrics.length > 0 ? (
    <div className="border-t border-border px-4 py-3">
      <p className="mb-2 flex items-center gap-1.5 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
        <Gauge className="size-3" /> Metric exemplars
      </p>
      <div className="divide-y divide-border/60">
        {metrics.map((metric) => (
          <div
            className="flex items-center justify-between gap-4 py-2 text-[9px]"
            key={`${metric.name}:${metric.timeUnixNano}`}
          >
            <span className="truncate">{metric.name}</span>
            <span className="shrink-0 text-muted-foreground tabular-nums">
              {metric.value === null ? "—" : metric.value.toLocaleString()}{" "}
              {metric.unit}
            </span>
          </div>
        ))}
      </div>
    </div>
  ) : null;

const TraceAttributeDetails = ({
  metrics,
  onOpenError,
  onOpenLogs,
  onOpenTrace,
  onSelectSpan,
  span,
  traceSpans,
}: {
  metrics: ServiceTraceMetricSample[];
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  onOpenTrace?: (traceID: string, segmentID?: string) => void;
  onSelectSpan: (spanID: string) => void;
  span: ServiceTraceSpan;
  traceSpans: ServiceTraceSpan[];
}) => {
  const spanPayload = asRecord(span.span);
  const groups = traceAttributeGroups(span);
  const scope = asRecord(span.scope);
  const service =
    otlpAttribute(span.resource, "service.name") ?? span.serviceId;
  const operation =
    text(spanPayload?.op) ??
    text(asRecord(asRecord(spanPayload?.contexts)?.trace)?.op);
  const marker = errorMarker(span);
  const links = linkedTraces(span);
  const spanMetrics = metrics.filter(
    (metric) => !metric.spanId || metric.spanId === span.spanId
  );
  return (
    <>
      {span.aiKind ? (
        <AiSpanDetails span={span} traceSpans={traceSpans} />
      ) : null}
      <TraceSpanActions
        marker={marker}
        onOpenError={onOpenError}
        onOpenLogs={onOpenLogs}
        span={span}
      />
      <TraceSpanMetadata
        onSelectSpan={onSelectSpan}
        operation={operation}
        scope={scope}
        service={service}
        span={span}
        traceSpans={traceSpans}
      />
      <TraceLinks links={links} onOpenTrace={onOpenTrace} />
      <TraceMetricExemplars metrics={spanMetrics} />
      <TraceSpanContext span={span} />
      {groups.map((group) => (
        <div className="border-t border-border" key={group.label}>
          <p className="bg-muted/15 px-4 py-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            {group.label}
          </p>
          {group.values.map((attribute) => (
            <div
              className="grid grid-cols-[minmax(9rem,0.42fr)_minmax(0,1fr)] border-t border-border/60 text-[9px] first:border-t-0"
              key={`${group.label}:${attribute.key}`}
            >
              <code className="overflow-hidden px-4 py-2 text-ellipsis whitespace-nowrap text-muted-foreground">
                {attribute.key}
              </code>
              <code className="overflow-x-auto border-l border-border/60 px-4 py-2 text-foreground/80">
                {attribute.value}
              </code>
            </div>
          ))}
        </div>
      ))}
    </>
  );
};

const TraceAttributes = ({
  metrics,
  onOpenError,
  onOpenLogs,
  onOpenTrace,
  onSelectSpan,
  span,
  traceSpans,
  traceStart,
}: {
  metrics: ServiceTraceMetricSample[];
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  onOpenTrace?: (traceID: string, segmentID?: string) => void;
  onSelectSpan: (spanID: string) => void;
  span: ServiceTraceSpan;
  traceSpans: ServiceTraceSpan[];
  traceStart: bigint;
}) => {
  const [tab, setTab] = useState<"details" | "raw">("details");
  const raw = JSON.stringify(
    {
      resource: span.resource,
      scope: span.scope,
      source: span.source,
      span: span.span,
    },
    null,
    2
  );

  return (
    <aside className="min-h-0 border-l border-border max-lg:border-t max-lg:border-l-0">
      <TraceAttributeHeader span={span} traceStart={traceStart} />
      <nav
        aria-label="Span inspector"
        className="flex h-9 items-end border-t border-b border-border px-3"
      >
        {(["details", "raw"] as const).map((value) => (
          <button
            className={cn(
              "h-full border-b-2 border-transparent px-3 text-[9px] text-muted-foreground hover:text-foreground",
              tab === value && "border-foreground text-foreground"
            )}
            key={value}
            onClick={() => setTab(value)}
            type="button"
          >
            {value === "details" ? "Details" : "Raw data"}
          </button>
        ))}
      </nav>
      {tab === "raw" ? (
        <pre className="max-h-[44rem] overflow-auto px-4 py-3 font-mono text-[9px] leading-relaxed whitespace-pre-wrap text-foreground/80">
          {raw}
        </pre>
      ) : (
        <TraceAttributeDetails
          metrics={metrics}
          onOpenError={onOpenError}
          onOpenLogs={onOpenLogs}
          onOpenTrace={onOpenTrace}
          onSelectSpan={onSelectSpan}
          span={span}
          traceSpans={traceSpans}
        />
      )}
    </aside>
  );
};

const vitalStatusClass = (status: WebVitalMeasurement["status"]) => {
  if (status === "good") {
    return "border-emerald-500/45 bg-emerald-500/10 text-emerald-500";
  }
  if (status === "needs-improvement") {
    return "border-amber-500/45 bg-amber-500/10 text-amber-500";
  }
  return "border-destructive/45 bg-destructive/10 text-destructive";
};

const TraceVitals = ({ detail }: { detail: ServiceTraceDetail }) => {
  const values = traceWebVitals(detail.spans);
  if (values.length === 0) {
    return null;
  }
  const byKey = new Map(values.map((value) => [value.key, value]));
  return (
    <section className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-border px-4 py-2.5 lg:px-6">
      <p className="mr-1 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
        Web Vitals
      </p>
      {webVitalKeys.map((key) => {
        const vital = byKey.get(key);
        return (
          <div
            className="flex h-6 items-stretch"
            key={key}
            title={`${webVitalName(key)}${vital ? ` · ${vital.status.replace("-", " ")}` : ""}`}
          >
            <span
              className={cn(
                "grid min-w-9 place-items-center border px-2 text-[8px] font-medium uppercase",
                vital
                  ? vitalStatusClass(vital.status)
                  : "border-border text-muted-foreground"
              )}
            >
              {key}
            </span>
            <span className="grid min-w-14 place-items-center border border-l-0 border-border px-2 text-[9px] tabular-nums">
              {vital ? formatWebVital(vital) : "—"}
            </span>
          </div>
        );
      })}
    </section>
  );
};

const TimelineIndicators = ({
  timeline,
  traceStart,
  vitals,
}: {
  timeline: ReturnType<typeof buildTraceTimeline>;
  traceStart: bigint;
  vitals: WebVitalMeasurement[];
}) => (
  <>
    {vitals.flatMap((vital) => {
      if (
        !(vital.key === "ttfb" || vital.key === "fcp" || vital.key === "lcp")
      ) {
        return [];
      }
      const measurement = vital.valueMilliseconds;
      const timestamp =
        measurement === undefined
          ? undefined
          : traceStart + BigInt(Math.round(measurement * 1_000_000));
      const left =
        timestamp === undefined ||
        timestamp < timeline.viewport.start ||
        timestamp > timeline.viewport.end
          ? undefined
          : timeline.position(timestamp);
      return left === undefined
        ? []
        : [
            <span
              aria-hidden="true"
              className={cn(
                "pointer-events-none absolute inset-y-0 z-10 w-px",
                vital.status === "poor"
                  ? "bg-destructive/60"
                  : "bg-foreground/25"
              )}
              key={vital.key}
              style={{ left: `${left}%` }}
              title={`${vital.key.toUpperCase()} ${formatWebVital(vital)}`}
            />,
          ];
    })}
  </>
);

const longestMeasurementDuration = (vitals: WebVitalMeasurement[]) => {
  let longest = 0n;
  for (const vital of vitals) {
    if (vital.valueMilliseconds === undefined) {
      continue;
    }
    const duration = BigInt(Math.round(vital.valueMilliseconds * 1_000_000));
    if (duration > longest) {
      longest = duration;
    }
  }
  return longest;
};

const traceRowTitle = (row: TraceRow) => {
  if (row.kind !== "span") {
    return row.label;
  }
  return row.span.aiKind ? aiSpanLabel(row.span) : row.span.name;
};

const traceRowSubtitle = (
  row: TraceRow,
  service?: string,
  operation?: string
) => {
  if (row.kind === "gap") {
    return "unaccounted time inside parent span";
  }
  if (row.kind === "group") {
    return `${row.groupedSpans.length} repeated sibling spans`;
  }
  if (row.span.aiKind) {
    return row.span.aiOperation || row.span.aiKind;
  }
  return service ?? operation ?? spanKind(row.span.kind);
};

const traceRowBarClass = (row: TraceRow) => {
  if (row.kind === "gap") {
    return "border border-dashed border-amber-500/70 bg-amber-500/10";
  }
  return row.kind === "group" ? "bg-sky-500/55" : spanBarColor(row.span);
};

const TraceWaterfallRow = ({
  baseViewportStart,
  collapsed,
  expandedGroups,
  onSelect,
  onToggleCollapsed,
  onToggleGroup,
  row,
  selectedID,
  timeline,
  vitals,
}: {
  baseViewportStart: bigint;
  collapsed: ReadonlySet<string>;
  expandedGroups: ReadonlySet<string>;
  onSelect: (spanID: string) => void;
  onToggleCollapsed: (spanID: string) => void;
  onToggleGroup: (groupID: string) => void;
  row: TraceRow;
  selectedID?: string;
  timeline: ReturnType<typeof buildTraceTimeline>;
  vitals: WebVitalMeasurement[];
}) => {
  const { childCount, depth, span } = row;
  const left = timeline.position(integer(row.startTimeUnixNano));
  const right = timeline.position(integer(row.endTimeUnixNano));
  const width = Math.min(100 - left, Math.max(0.35, right - left));
  const service = otlpAttribute(span.resource, "service.name");
  const operation = text(asRecord(span.span)?.op);
  const isPoint = integer(row.durationNano) === 0n;
  const groupExpanded = expandedGroups.has(row.id);
  let expander = <span className="size-4 shrink-0" />;
  if (row.kind === "group") {
    expander = (
      <button
        aria-label={groupExpanded ? "Collapse span group" : "Expand span group"}
        className="grid size-4 shrink-0 place-items-center text-muted-foreground hover:text-foreground"
        onClick={() => onToggleGroup(row.id)}
        type="button"
      >
        <ChevronRight
          className={cn(
            "size-3 transition-transform",
            groupExpanded && "rotate-90"
          )}
        />
      </button>
    );
  } else if (childCount > 0) {
    expander = (
      <button
        aria-label={
          collapsed.has(span.spanId)
            ? "Expand child spans"
            : "Collapse child spans"
        }
        className="grid size-4 shrink-0 place-items-center text-muted-foreground hover:text-foreground"
        onClick={() => onToggleCollapsed(span.spanId)}
        type="button"
      >
        <ChevronRight
          className={cn(
            "size-3 transition-transform",
            !collapsed.has(span.spanId) && "rotate-90"
          )}
        />
      </button>
    );
  }
  const selectRow = () => {
    if (row.kind === "group") {
      onToggleGroup(row.id);
    } else if (row.kind === "span") {
      onSelect(span.spanId);
    }
  };
  return (
    <div
      className={cn(
        "grid min-h-10 w-full grid-cols-[minmax(15rem,0.44fr)_minmax(20rem,1fr)_6rem] items-center border-b border-border/70 px-4 text-left text-[9px] hover:bg-muted/30",
        selectedID === span.spanId && "bg-muted/35"
      )}
    >
      <span
        className="flex min-w-0 items-center gap-2"
        style={{ paddingLeft: `${depth * 14}px` }}
      >
        {expander}
        <button
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          onClick={selectRow}
          type="button"
        >
          {row.kind === "gap" ? (
            <CircleDashed className="size-3 shrink-0 text-amber-500" />
          ) : (
            <SpanMarker span={span} />
          )}
          <span className="min-w-0">
            <span className="block truncate">{traceRowTitle(row)}</span>
            <span className="block truncate text-[8px] text-muted-foreground">
              {traceRowSubtitle(row, service, operation)}
            </span>
          </span>
        </button>
      </span>
      <button
        aria-label={`Inspect ${span.name}`}
        className="relative mr-4 h-5 bg-[linear-gradient(to_right,var(--border)_1px,transparent_1px)] bg-[length:25%_100%]"
        onClick={() => onSelect(span.spanId)}
        type="button"
      >
        <TimelineIndicators
          timeline={timeline}
          traceStart={baseViewportStart}
          vitals={vitals}
        />
        <span
          className={cn(
            "absolute z-20",
            isPoint
              ? "top-1.5 size-2 -translate-x-1/2 rotate-45"
              : "top-1.5 h-2 min-w-px",
            traceRowBarClass(row)
          )}
          style={{
            left: `${left}%`,
            width: isPoint ? undefined : `${width}%`,
          }}
        />
      </button>
      <button
        className="text-right text-muted-foreground tabular-nums"
        onClick={() => onSelect(span.spanId)}
        type="button"
      >
        {formatDuration(row.durationNano)}
      </button>
    </div>
  );
};

const TraceWaterfall = ({
  detail,
  onOpenError,
  onOpenLogs,
  onOpenTrace,
}: {
  detail: ServiceTraceDetail;
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  onOpenTrace?: (traceID: string, segmentID?: string) => void;
}) => {
  const [selectedID, setSelectedID] = useState(detail.spans[0]?.spanId ?? "");
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(
    () => new Set()
  );
  const [autoGroup, setAutoGroup] = useState(true);
  const [showGaps, setShowGaps] = useState(true);
  const [compressGaps, setCompressGaps] = useState(true);
  const [query, setQuery] = useState("");
  const vitals = useMemo(() => traceWebVitals(detail.spans), [detail.spans]);
  const baseViewport = useMemo(() => {
    const bounds = traceViewport(detail.spans);
    const measurementDuration = longestMeasurementDuration(vitals);
    return measurementDuration > bounds.end - bounds.start
      ? { ...bounds, end: bounds.start + measurementDuration }
      : bounds;
  }, [detail.spans, vitals]);
  const [viewport, setViewport] = useState<TraceViewport>(baseViewport);
  const timeline = useMemo(
    () => buildTraceTimeline(detail.spans, viewport, compressGaps),
    [compressGaps, detail.spans, viewport]
  );
  const rows = useMemo(
    () =>
      traceRows(detail.spans, collapsed, query, {
        autoGroup,
        expandedGroups,
        showGaps,
      }),
    [autoGroup, collapsed, detail.spans, expandedGroups, query, showGaps]
  );
  const visibleRows = useMemo(
    () =>
      rows.filter(
        (row) =>
          BigInt(row.endTimeUnixNano) >= viewport.start &&
          BigInt(row.startTimeUnixNano) <= viewport.end
      ),
    [rows, viewport.end, viewport.start]
  );
  const matches = useMemo(
    () => matchingTraceSpans(detail.spans, query),
    [detail.spans, query]
  );
  const selected =
    detail.spans.find((span) => span.spanId === selectedID) ?? detail.spans[0];
  const collapsibleIDs = useMemo(
    () =>
      new Set(
        traceRows(detail.spans, new Set(), "")
          .filter((row) => row.kind === "span" && row.childCount > 0)
          .map((row) => row.span.spanId)
      ),
    [detail.spans]
  );
  const toggleCollapsed = (spanID: string) => {
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(spanID)) {
        next.delete(spanID);
      } else {
        next.add(spanID);
      }
      return next;
    });
  };
  const toggleGroup = (groupID: string) => {
    setExpandedGroups((current) => {
      const next = new Set(current);
      if (next.has(groupID)) {
        next.delete(groupID);
      } else {
        next.add(groupID);
      }
      return next;
    });
  };
  const moveMatch = (direction: -1 | 1) => {
    if (matches.length === 0) {
      return;
    }
    const current = matches.findIndex((span) => span.spanId === selectedID);
    let next = (current + direction + matches.length) % matches.length;
    if (current === -1) {
      next = direction > 0 ? 0 : matches.length - 1;
    }
    const match = matches[next];
    if (match) {
      setSelectedID(match.spanId);
    }
  };
  const zoom = (direction: "in" | "out") => {
    setViewport((current) => {
      const { end: baseEnd, start: baseStart } = baseViewport;
      const baseDuration = baseEnd - baseStart;
      const currentDuration = current.end - current.start;
      const desired =
        direction === "in" ? currentDuration / 2n : currentDuration * 2n;
      let duration = desired;
      if (duration < 1_000_000n) {
        duration = 1_000_000n;
      } else if (duration > baseDuration) {
        duration = baseDuration;
      }
      const center = selected
        ? (BigInt(selected.startTimeUnixNano) +
            BigInt(selected.endTimeUnixNano)) /
          2n
        : (current.start + current.end) / 2n;
      let start = center - duration / 2n;
      let end = start + duration;
      if (start < baseStart) {
        start = baseStart;
        end = start + duration;
      }
      if (end > baseEnd) {
        end = baseEnd;
        start = end - duration;
      }
      return { end, start };
    });
  };
  const resetZoom = () => setViewport(baseViewport);
  const isZoomed =
    viewport.start !== baseViewport.start || viewport.end !== baseViewport.end;
  const selectedMatch = matches.findIndex((span) => span.spanId === selectedID);

  return (
    <div className="grid border-b border-border lg:grid-cols-[minmax(0,1fr)_minmax(24rem,0.42fr)]">
      <section className="min-w-0">
        <div className="flex min-h-11 items-center gap-2 overflow-x-auto border-b border-border px-3 py-1.5">
          <div className="relative min-w-48 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3 -translate-y-1/2 text-muted-foreground" />
            <Input
              aria-label="Search spans"
              className="h-7 border-0 bg-transparent pl-7 text-[9px] shadow-none"
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search or use duration:>500ms status:error service:api has:issue"
              type="search"
              value={query}
            />
          </div>
          <span className="text-[8px] text-muted-foreground tabular-nums">
            {query
              ? `${Math.max(0, selectedMatch + 1)}/${matches.length} matches`
              : `${detail.spans.length} spans`}
          </span>
          {query ? (
            <>
              <Button
                aria-label="Previous matching span"
                disabled={matches.length === 0}
                onClick={() => moveMatch(-1)}
                size="icon"
                variant="ghost"
              >
                <ChevronUp />
              </Button>
              <Button
                aria-label="Next matching span"
                disabled={matches.length === 0}
                onClick={() => moveMatch(1)}
                size="icon"
                variant="ghost"
              >
                <ChevronDown />
              </Button>
            </>
          ) : null}
          <Button
            aria-pressed={autoGroup}
            onClick={() => setAutoGroup((value) => !value)}
            size="sm"
            variant={autoGroup ? "secondary" : "ghost"}
          >
            Group
          </Button>
          <Button
            aria-pressed={showGaps}
            onClick={() => setShowGaps((value) => !value)}
            size="sm"
            variant={showGaps ? "secondary" : "ghost"}
          >
            Gaps
          </Button>
          <Button
            aria-pressed={compressGaps}
            onClick={() => setCompressGaps((value) => !value)}
            size="sm"
            variant={compressGaps ? "secondary" : "ghost"}
          >
            Compress
          </Button>
          <Button
            aria-label="Zoom in"
            onClick={() => zoom("in")}
            size="icon"
            variant="ghost"
          >
            <ZoomIn />
          </Button>
          <Button
            aria-label="Zoom out"
            disabled={!isZoomed}
            onClick={() => zoom("out")}
            size="icon"
            variant="ghost"
          >
            <ZoomOut />
          </Button>
          <Button
            aria-label="Reset zoom"
            disabled={!isZoomed}
            onClick={resetZoom}
            size="icon"
            variant="ghost"
          >
            <RotateCcw />
          </Button>
          <Button
            aria-label="Collapse all spans"
            disabled={query !== ""}
            onClick={() => setCollapsed(new Set(collapsibleIDs))}
            size="icon"
            title="Collapse all"
            variant="ghost"
          >
            <ChevronsDownUp />
          </Button>
          <Button
            aria-label="Expand all spans"
            disabled={query !== ""}
            onClick={() => setCollapsed(new Set())}
            size="icon"
            title="Expand all"
            variant="ghost"
          >
            <ChevronsUpDown />
          </Button>
        </div>
        <div className="overflow-x-auto">
          <div className="min-w-[760px]">
            <div className="grid h-10 grid-cols-[minmax(15rem,0.44fr)_minmax(20rem,1fr)_6rem] items-center border-b border-border px-4 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
              <span>Service / span</span>
              <span className="relative grid h-full grid-cols-5 items-center text-center tracking-normal normal-case">
                <TimelineIndicators
                  timeline={timeline}
                  traceStart={baseViewport.start}
                  vitals={vitals}
                />
                {[0, 25, 50, 75, 100].map((percent) => (
                  <span key={percent}>
                    +
                    {formatDuration(
                      (timeline.timeAt(percent) - baseViewport.start).toString()
                    )}
                  </span>
                ))}
              </span>
              <span className="text-right">Duration</span>
            </div>
            {visibleRows.map((row) => (
              <TraceWaterfallRow
                baseViewportStart={baseViewport.start}
                collapsed={collapsed}
                expandedGroups={expandedGroups}
                key={row.id}
                onSelect={setSelectedID}
                onToggleCollapsed={toggleCollapsed}
                onToggleGroup={toggleGroup}
                row={row}
                selectedID={selected?.spanId}
                timeline={timeline}
                vitals={vitals}
              />
            ))}
          </div>
        </div>
      </section>
      {selected ? (
        <TraceAttributes
          key={selected.spanId}
          metrics={detail.metrics}
          onOpenError={onOpenError}
          onOpenLogs={onOpenLogs}
          onOpenTrace={onOpenTrace}
          onSelectSpan={setSelectedID}
          span={selected}
          traceSpans={detail.spans}
          traceStart={baseViewport.start}
        />
      ) : null}
    </div>
  );
};

const TraceOverview = ({ detail }: { detail: ServiceTraceDetail }) => {
  const bounds = traceBounds(detail.spans);
  const root =
    detail.spans.find((span) => span.isSegment) ??
    detail.spans.find((span) => !span.parentSpanId) ??
    detail.spans[0];
  const payload = sentryTracePayload(detail.spans);
  const environment =
    text(payload?.environment) ??
    otlpAttribute(root?.resource, "deployment.environment.name") ??
    otlpAttribute(root?.resource, "deployment.environment");
  const release =
    text(payload?.release) ?? otlpAttribute(root?.resource, "service.version");
  const errors = detail.spans.filter((span) => span.statusCode === 2).length;
  const entries = [
    [
      "Started",
      root ? nanosToDate(root.startTimeUnixNano).toLocaleString() : "—",
    ],
    ["Duration", formatDuration(bounds.duration.toString())],
    ["Spans", detail.spans.length.toLocaleString()],
    ["Errors", errors.toLocaleString()],
    ["Environment", environment ?? "—"],
    ["Release", release ?? "—"],
  ];
  return (
    <dl className="grid grid-cols-6 border-b border-border max-xl:grid-cols-3 max-sm:grid-cols-2">
      {entries.map(([label, value]) => (
        <div
          className="border-r border-border px-4 py-3 last:border-r-0"
          key={label}
        >
          <dt className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            {label}
          </dt>
          <dd
            className={cn(
              "mt-1 truncate text-[10px] tabular-nums",
              label === "Errors" && errors > 0 && "text-destructive"
            )}
            title={value}
          >
            {value}
          </dd>
        </div>
      ))}
    </dl>
  );
};

const RelatedTraceSegments = ({
  detail,
  onOpenTrace,
}: {
  detail: ServiceTraceDetail;
  onOpenTrace?: (traceID: string, segmentID?: string) => void;
}) =>
  detail.relatedSegments.length > 0 ? (
    <section className="border-b border-border">
      <header className="flex items-center justify-between gap-3 px-4 py-2 lg:px-6">
        <p className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
          Related transactions
        </p>
        <span className="text-[8px] text-muted-foreground tabular-nums">
          {detail.relatedSegments.length.toLocaleString()}
        </span>
      </header>
      <div className="flex overflow-x-auto border-t border-border/70">
        {detail.relatedSegments.map((segment) => (
          <button
            className="min-w-56 border-r border-border px-4 py-2.5 text-left transition-colors last:border-r-0 hover:bg-muted/30 disabled:cursor-default"
            disabled={!onOpenTrace}
            key={segment.segmentId}
            onClick={() => onOpenTrace?.(detail.traceId, segment.segmentId)}
            type="button"
          >
            <span className="block truncate text-[9px] font-medium">
              {segment.name || "Unnamed transaction"}
            </span>
            <span className="mt-1 block truncate text-[8px] text-muted-foreground">
              {segment.serviceId} · {formatDuration(segment.durationNano)} ·{" "}
              {segment.spanCount.toLocaleString()} spans
            </span>
          </button>
        ))}
      </div>
    </section>
  ) : null;

export const ServiceTraceDetailView = ({
  detail,
  onBack,
  onOpenError,
  onOpenLogs,
  onOpenTrace,
  scope,
}: {
  detail: ServiceTraceDetail;
  onBack: () => void;
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  onOpenTrace?: (traceID: string, segmentID?: string) => void;
  scope: MetricScope;
}) => {
  const root =
    detail.spans.find((span) => span.isSegment) ??
    detail.spans.find((span) => !span.parentSpanId) ??
    detail.spans[0];
  const payload = sentryTracePayload(detail.spans);
  const payloadServiceID = detail.spans.find(
    (span) => span.source === "sentry_error"
  )?.serviceId;
  const serviceID =
    payloadServiceID ??
    root?.serviceId ??
    (scope.kind === "service" ? scope.serviceID : undefined);
  const projectID = scope.kind === "installation" ? undefined : scope.projectID;
  const relatedReplayID = replayID(payload);
  const loadRecording = useCallback(
    (id: string, signal: AbortSignal) => {
      if (!(projectID && serviceID)) {
        return Promise.reject(
          new Error("Replay service context is unavailable")
        );
      }
      return fetchServiceReplayRecording(projectID, serviceID, id, signal);
    },
    [projectID, serviceID]
  );

  return (
    <div>
      <header className="flex flex-wrap items-start gap-3 border-b border-border px-4 py-4 lg:px-6">
        <Button onClick={onBack} size="sm" variant="ghost">
          <ArrowLeft /> Traces
        </Button>
        <div className="min-w-0">
          <h2 className="truncate text-sm font-medium">
            {root?.name ?? "Trace"}
          </h2>
          <p className="mt-1 font-mono text-[9px] text-muted-foreground">
            {detail.traceId}
          </p>
        </div>
      </header>
      <TraceOverview detail={detail} />
      <RelatedTraceSegments detail={detail} onOpenTrace={onOpenTrace} />
      <TraceVitals detail={detail} />
      <TraceWaterfall
        detail={detail}
        onOpenError={onOpenError}
        onOpenLogs={onOpenLogs}
        onOpenTrace={onOpenTrace}
      />
      {detail.profiles.length > 0 ? (
        <TraceProfileFlamegraph
          profiles={detail.profiles}
          spans={detail.spans}
        />
      ) : null}
      {onOpenLogs ? (
        <TraceRelatedLogs
          onOpenLogs={(traceID) => onOpenLogs(traceID)}
          scope={scope}
          traceID={detail.traceId}
        />
      ) : null}
      {payload ? (
        <div className="px-5 lg:px-7">
          {relatedReplayID && projectID && serviceID ? (
            <RelatedReplay
              appId={serviceID}
              loadRecording={loadRecording}
              onOpenTrace={onOpenTrace}
              replayId={relatedReplayID}
            />
          ) : null}
          <BreadcrumbsSection payload={payload} />
          <EventMessageSection payload={payload} />
          <EventEnvironmentSection payload={payload} />
          <EventRequestSection payload={payload} />
          <ContextSection payload={payload} />
        </div>
      ) : null}
    </div>
  );
};
