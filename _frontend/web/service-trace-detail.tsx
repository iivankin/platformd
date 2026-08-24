import { Dialog } from "@base-ui/react/dialog";
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
  X,
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
import { asRecord, recordRows } from "@/errors/event-context";
import { RelatedReplay } from "@/errors/related-replay";
import { cn } from "@/lib/utils";
import { otlpAttributeText, otlpTextAttributes } from "@/otlp";
import {
  collapsibleTraceSpanIDs,
  formatWebVital,
  traceRows,
  traceRoot,
  traceWebVitals,
  webVitalTimelineMarkers,
  webVitalKeys,
  webVitalName,
} from "@/trace-details-model";
import type {
  TraceRow,
  WebVitalMeasurement,
  WebVitalTimelineMarker,
} from "@/trace-details-model";
import {
  formatTraceDuration as formatDuration,
  traceInteger as integer,
  traceNanosToDate as nanosToDate,
} from "@/trace-format";
import { TraceRelatedLogs } from "@/trace-related-logs";
import { matchingTraceSpans } from "@/trace-search";
import { traceServiceName } from "@/trace-service-name";
import type { ServiceNameResolver } from "@/trace-service-name";
import { traceSpanSelfTime } from "@/trace-span-context";
import { TraceSpanContext } from "@/trace-span-context-view";
import {
  buildTraceTimeline,
  traceViewport,
  zoomTraceViewport,
} from "@/trace-timeline";
import type { TraceViewport } from "@/trace-timeline";

const formatDurationNanos = (value: number) =>
  formatDuration(Math.max(0, Math.round(value)).toString());

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
  return span.statusMessage || "no error reported";
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
    { label: "Span attributes", values: otlpTextAttributes(span.span) },
    { label: "Span data", values: rowValues(payload?.data) },
    { label: "Measurements", values: rowValues(payload?.measurements) },
    { label: "Resource", values: otlpTextAttributes(span.resource) },
    { label: "Instrumentation", values: otlpTextAttributes(span.scope) },
  ].filter((group) => group.values.length > 0);
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

const TraceAiIdentity = ({ span }: { span: ServiceTraceSpan }) => (
  <>
    {span.aiUserId ? (
      <>
        <dt className="text-muted-foreground">AI user</dt>
        <dd className="overflow-x-auto font-mono">{span.aiUserId}</dd>
      </>
    ) : null}
    {span.aiSessionId ? (
      <>
        <dt className="text-muted-foreground">AI session</dt>
        <dd className="overflow-x-auto font-mono">{span.aiSessionId}</dd>
      </>
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
  const parentSpanLoaded = traceSpans.some(
    (candidate) => candidate.spanId === span.parentSpanId
  );
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
      <TraceAiIdentity span={span} />
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
        {parentSpanLoaded ? (
          <button
            className="text-sky-600 hover:underline dark:text-sky-400"
            onClick={() => onSelectSpan(span.parentSpanId)}
            type="button"
          >
            {span.parentSpanId}
          </button>
        ) : (
          span.parentSpanId || "root"
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
  onOpenTrace?: (traceID: string) => void;
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
  serviceName,
  span,
  traceSpans,
}: {
  metrics: ServiceTraceMetricSample[];
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  onOpenTrace?: (traceID: string) => void;
  onSelectSpan: (spanID: string) => void;
  serviceName?: ServiceNameResolver;
  span: ServiceTraceSpan;
  traceSpans: ServiceTraceSpan[];
}) => {
  const spanPayload = asRecord(span.span);
  const groups = traceAttributeGroups(span);
  const scope = asRecord(span.scope);
  const service = traceServiceName(span, serviceName);
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

const TraceSpanDialog = ({
  metrics,
  onOpenError,
  onOpenLogs,
  onOpenTrace,
  onOpenChange,
  onSelectSpan,
  open,
  serviceName,
  span,
  traceSpans,
  traceStart,
}: {
  metrics: ServiceTraceMetricSample[];
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  onOpenTrace?: (traceID: string) => void;
  onOpenChange: (open: boolean) => void;
  onSelectSpan: (spanID: string) => void;
  open: boolean;
  serviceName?: ServiceNameResolver;
  span: ServiceTraceSpan;
  traceSpans: ServiceTraceSpan[];
  traceStart: bigint;
}) => {
  const [tab, setTab] = useState<"details" | "raw">("details");
  const raw = JSON.stringify(
    {
      indexed: {
        aiAgent: span.aiAgent,
        aiKind: span.aiKind,
        aiModel: span.aiModel,
        aiOperation: span.aiOperation,
        aiProvider: span.aiProvider,
        aiSessionId: span.aiSessionId,
        aiUserId: span.aiUserId,
      },
      resource: span.resource,
      scope: span.scope,
      source: span.source,
      span: span.span,
    },
    null,
    2
  );
  const offset = integer(span.startTimeUnixNano) - traceStart;
  const spanNavigation = useMemo(() => {
    const byID = new Map(traceSpans.map((item) => [item.spanId, item]));
    const depth = (item: ServiceTraceSpan) => {
      let current = item;
      let value = 0;
      const visited = new Set<string>();
      while (current.parentSpanId && value < 12) {
        if (visited.has(current.spanId)) {
          break;
        }
        visited.add(current.spanId);
        const parent = byID.get(current.parentSpanId);
        if (!parent) {
          break;
        }
        value += 1;
        current = parent;
      }
      return value;
    };
    return traceSpans
      .map((item) => ({ depth: depth(item), span: item }))
      .toSorted((left, right) => {
        const leftStart = integer(left.span.startTimeUnixNano);
        const rightStart = integer(right.span.startTimeUnixNano);
        if (leftStart < rightStart) {
          return -1;
        }
        if (leftStart > rightStart) {
          return 1;
        }
        return 0;
      });
  }, [traceSpans]);

  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        onOpenChange(nextOpen);
        if (!nextOpen) {
          setTab("details");
        }
      }}
      open={open}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
        <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
          <Dialog.Popup className="flex h-[calc(100dvh-2rem)] w-full max-w-[calc(100vw-2rem)] flex-col border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out">
            <header className="flex items-start justify-between gap-5 border-b border-border px-5 py-4">
              <div className="min-w-0">
                <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                  {span.aiKind || "Selected span"}
                </p>
                <Dialog.Title className="mt-1 truncate text-sm font-medium">
                  {span.aiKind ? aiSpanLabel(span) : span.name}
                </Dialog.Title>
                <Dialog.Description className="mt-1.5 text-[9px] text-muted-foreground">
                  {spanKind(span.kind)} · {formatDuration(span.durationNano)} ·
                  +{formatDuration(offset.toString())}
                </Dialog.Description>
              </div>
              <Dialog.Close
                aria-label="Close span details"
                className="flex size-8 shrink-0 items-center justify-center text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring"
              >
                <X className="size-4" />
              </Dialog.Close>
            </header>
            <nav
              aria-label="Span inspector"
              className="flex h-9 shrink-0 items-end border-b border-border px-3"
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
            <div className="min-h-0 flex-1 overflow-hidden">
              {tab === "raw" ? (
                <pre className="h-full overflow-auto px-4 py-3 font-mono text-[9px] leading-relaxed whitespace-pre-wrap text-foreground/80">
                  {raw}
                </pre>
              ) : (
                <div className="grid h-full min-h-0 md:grid-cols-[17rem_minmax(0,1fr)]">
                  <aside className="hidden min-h-0 overflow-y-auto border-r border-border md:block">
                    <p className="sticky top-0 z-10 border-b border-border bg-background px-3 py-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
                      Trace spans · {traceSpans.length.toLocaleString()}
                    </p>
                    {spanNavigation.map((item) => (
                      <button
                        className={cn(
                          "flex w-full items-center gap-2 border-b border-border/50 px-2 py-2 text-left text-[9px] hover:bg-muted/40",
                          item.span.spanId === span.spanId && "bg-muted/60"
                        )}
                        key={item.span.spanId}
                        onClick={() => onSelectSpan(item.span.spanId)}
                        style={{
                          paddingLeft: `${8 + Math.min(item.depth, 8) * 10}px`,
                        }}
                        type="button"
                      >
                        <span
                          className={cn(
                            "size-1.5 shrink-0 bg-muted-foreground/45",
                            item.span.aiKind && "bg-violet-500",
                            item.span.statusCode === 2 && "bg-destructive"
                          )}
                        />
                        <span className="min-w-0 flex-1 truncate">
                          {item.span.aiKind
                            ? aiSpanLabel(item.span)
                            : item.span.name}
                        </span>
                        <span className="shrink-0 text-[8px] text-muted-foreground tabular-nums">
                          {formatDuration(item.span.durationNano)}
                        </span>
                      </button>
                    ))}
                  </aside>
                  <div className="min-h-0 overflow-y-auto">
                    <TraceAttributeDetails
                      metrics={metrics}
                      onOpenError={onOpenError}
                      onOpenLogs={onOpenLogs}
                      onOpenTrace={onOpenTrace}
                      onSelectSpan={onSelectSpan}
                      serviceName={serviceName}
                      span={span}
                      traceSpans={traceSpans}
                    />
                  </div>
                </div>
              )}
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
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
  const values = traceWebVitals(detail.webVitals);
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
  markers,
}: {
  timeline: ReturnType<typeof buildTraceTimeline>;
  markers: WebVitalTimelineMarker[];
}) => (
  <>
    {markers.flatMap(({ timestampUnixNano: timestamp, vital }) => {
      const left =
        timestamp < timeline.viewport.start || timestamp > timeline.viewport.end
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
  collapsed,
  expandedGroups,
  onSelect,
  onToggleCollapsed,
  onToggleGroup,
  row,
  serviceName,
  selectedID,
  timeline,
  vitalMarkers,
}: {
  collapsed: ReadonlySet<string>;
  expandedGroups: ReadonlySet<string>;
  onSelect: (spanID: string) => void;
  onToggleCollapsed: (spanID: string) => void;
  onToggleGroup: (groupID: string) => void;
  row: TraceRow;
  serviceName?: ServiceNameResolver;
  selectedID?: string;
  timeline: ReturnType<typeof buildTraceTimeline>;
  vitalMarkers: WebVitalTimelineMarker[];
}) => {
  const { childCount, depth, span } = row;
  const left = timeline.position(integer(row.startTimeUnixNano));
  const right = timeline.position(integer(row.endTimeUnixNano));
  const width = Math.min(100 - left, Math.max(0.35, right - left));
  const service = traceServiceName(span, serviceName);
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
        <TimelineIndicators markers={vitalMarkers} timeline={timeline} />
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
  serviceName,
}: {
  detail: ServiceTraceDetail;
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  onOpenTrace?: (traceID: string) => void;
  serviceName?: ServiceNameResolver;
}) => {
  const [selectedID, setSelectedID] = useState(
    () => traceRoot(detail.spans)?.spanId ?? ""
  );
  const [spanDialogOpen, setSpanDialogOpen] = useState(false);
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(
    () => new Set()
  );
  const [autoGroup, setAutoGroup] = useState(true);
  const [showGaps, setShowGaps] = useState(true);
  const [compressGaps, setCompressGaps] = useState(true);
  const [query, setQuery] = useState("");
  const vitals = useMemo(
    () => traceWebVitals(detail.webVitals),
    [detail.webVitals]
  );
  const traceSpanViewport = useMemo(
    () => traceViewport(detail.spans),
    [detail.spans]
  );
  const vitalMarkers = useMemo(
    () =>
      webVitalTimelineMarkers(vitals, detail.spans, traceSpanViewport.start),
    [detail.spans, traceSpanViewport.start, vitals]
  );
  const baseViewport = useMemo(() => {
    const { end: spanEnd } = traceSpanViewport;
    let end = spanEnd;
    for (const { timestampUnixNano } of vitalMarkers) {
      if (timestampUnixNano > end) {
        end = timestampUnixNano;
      }
    }
    return { ...traceSpanViewport, end };
  }, [traceSpanViewport, vitalMarkers]);
  const [viewport, setViewport] = useState<TraceViewport>(baseViewport);
  const timeline = useMemo(
    () => buildTraceTimeline(detail.spans, viewport, compressGaps),
    [compressGaps, detail.spans, viewport]
  );
  const matches = useMemo(
    () => matchingTraceSpans(detail.spans, query, serviceName),
    [detail.spans, query, serviceName]
  );
  const matchingSpanIDs = useMemo(
    () => new Set(matches.map((span) => span.spanId)),
    [matches]
  );
  const rows = useMemo(
    () =>
      traceRows(detail.spans, collapsed, query, {
        autoGroup,
        expandedGroups,
        matchingSpanIDs,
        serviceName,
        showGaps,
      }),
    [
      autoGroup,
      collapsed,
      detail.spans,
      expandedGroups,
      matchingSpanIDs,
      query,
      serviceName,
      showGaps,
    ]
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
  const selected =
    detail.spans.find((span) => span.spanId === selectedID) ?? detail.spans[0];
  const collapsibleIDs = useMemo(
    () => collapsibleTraceSpanIDs(detail.spans),
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
  const selectSpan = (spanID: string) => {
    setSelectedID(spanID);
    setSpanDialogOpen(true);
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
      selectSpan(match.spanId);
    }
  };
  const zoom = (direction: "in" | "out") => {
    setViewport((current) => {
      const center = selected
        ? (BigInt(selected.startTimeUnixNano) +
            BigInt(selected.endTimeUnixNano)) /
          2n
        : (current.start + current.end) / 2n;
      return zoomTraceViewport(current, baseViewport, center, direction);
    });
  };
  const resetZoom = () => setViewport(baseViewport);
  const isZoomed =
    viewport.start !== baseViewport.start || viewport.end !== baseViewport.end;
  const selectedMatch = matches.findIndex((span) => span.spanId === selectedID);

  return (
    <div className="border-b border-border">
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
                  markers={vitalMarkers}
                  timeline={timeline}
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
                collapsed={collapsed}
                expandedGroups={expandedGroups}
                key={row.id}
                onSelect={selectSpan}
                onToggleCollapsed={toggleCollapsed}
                onToggleGroup={toggleGroup}
                row={row}
                serviceName={serviceName}
                selectedID={selected?.spanId}
                timeline={timeline}
                vitalMarkers={vitalMarkers}
              />
            ))}
          </div>
        </div>
      </section>
      {selected ? (
        <TraceSpanDialog
          metrics={detail.metrics}
          onOpenError={onOpenError}
          onOpenLogs={onOpenLogs}
          onOpenTrace={onOpenTrace}
          onOpenChange={setSpanDialogOpen}
          onSelectSpan={selectSpan}
          open={spanDialogOpen}
          serviceName={serviceName}
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
  const root = traceRoot(detail.spans);
  const environment =
    otlpAttributeText(root?.resource, "deployment.environment.name") ??
    otlpAttributeText(root?.resource, "deployment.environment");
  const release = otlpAttributeText(root?.resource, "service.version");
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

export const ServiceTraceDetailView = ({
  detail,
  onBack,
  onOpenError,
  onOpenLogs,
  onOpenTrace,
  scope,
  serviceName,
}: {
  detail: ServiceTraceDetail;
  onBack: () => void;
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  onOpenTrace?: (traceID: string) => void;
  scope: MetricScope;
  serviceName?: ServiceNameResolver;
}) => {
  const root = traceRoot(detail.spans);
  const replaySpan = detail.spans.find((span) => span.replayId);
  const errorServiceID = detail.spans.find(
    (span) => span.source === "sentry_error"
  )?.serviceId;
  const serviceID =
    replaySpan?.serviceId ??
    errorServiceID ??
    root?.serviceId ??
    (scope.kind === "service" ? scope.serviceID : undefined);
  const projectID = scope.kind === "installation" ? undefined : scope.projectID;
  const relatedReplayID = replaySpan?.replayId;
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
      <TraceVitals detail={detail} />
      <TraceWaterfall
        detail={detail}
        onOpenError={onOpenError}
        onOpenLogs={onOpenLogs}
        onOpenTrace={onOpenTrace}
        serviceName={serviceName}
      />
      {onOpenLogs ? (
        <TraceRelatedLogs
          onOpenLogs={(traceID) => onOpenLogs(traceID)}
          scope={scope}
          traceID={detail.traceId}
        />
      ) : null}
      {relatedReplayID && projectID && serviceID ? (
        <div className="px-5 lg:px-7">
          <RelatedReplay
            appId={serviceID}
            loadRecording={loadRecording}
            onOpenTrace={onOpenTrace}
            replayId={relatedReplayID}
          />
        </div>
      ) : null}
    </div>
  );
};
