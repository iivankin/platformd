import {
  ArrowLeft,
  Bot,
  BrainCircuit,
  CircleAlert,
  Wrench,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";

import { AiSpanDetails } from "@/ai-span-details";
import { aiSpanLabel } from "@/ai-trace";
import { fetchServiceReplayRecording } from "@/api";
import type { ServiceTraceDetail, ServiceTraceSpan } from "@/api";
import { Button } from "@/components/ui/button";
import { asRecord, eventTags } from "@/errors/event-context";
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
  return JSON.stringify(value);
};

const otlpAttributes = (value: unknown) => {
  const attributes = asRecord(value)?.attributes;
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

const sentryPayload = (detail: ServiceTraceDetail) => {
  const root =
    detail.spans.find(
      (span) => span.source === "sentry" && !span.parentSpanId
    ) ?? detail.spans.find((span) => span.source === "sentry");
  return root ? asRecord(root.span) : undefined;
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

const spanDepths = (spans: ServiceTraceSpan[]) => {
  const byID = new Map(spans.map((span) => [span.spanId, span]));
  return new Map(
    spans.map((span) => {
      let depth = 0;
      let parentID = span.parentSpanId;
      const visited = new Set([span.spanId]);
      while (parentID && depth < spans.length) {
        if (visited.has(parentID)) {
          break;
        }
        visited.add(parentID);
        const parent = byID.get(parentID);
        if (!parent) {
          break;
        }
        depth += 1;
        parentID = parent.parentSpanId;
      }
      return [span.spanId, depth] as const;
    })
  );
};

const SpanMarker = ({ span }: { span: ServiceTraceSpan }) => {
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
  return <span className="size-1.5 shrink-0 bg-sky-500" />;
};

const spanBarColor = (span: ServiceTraceSpan) => {
  if (span.statusCode === 2) {
    return "bg-destructive";
  }
  return span.aiKind ? "bg-violet-500" : "bg-sky-500";
};

const TraceAttributes = ({
  span,
  traceSpans,
  traceStart,
}: {
  span: ServiceTraceSpan;
  traceSpans: ServiceTraceSpan[];
  traceStart: bigint;
}) => {
  const groups = span.aiKind
    ? []
    : [
        { label: "Span attributes", values: otlpAttributes(span.span) },
        { label: "Resource", values: otlpAttributes(span.resource) },
        { label: "Instrumentation", values: otlpAttributes(span.scope) },
      ].filter((group) => group.values.length > 0);
  const scope = asRecord(span.scope);
  const offset = integer(span.startTimeUnixNano) - traceStart;
  const service = otlpAttribute(span.resource, "service.name");
  const operation = text(asRecord(span.span)?.op);
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
        <>
          {span.aiKind ? (
            <AiSpanDetails span={span} traceSpans={traceSpans} />
          ) : null}
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
            <dt className="text-muted-foreground">Instrumentation</dt>
            <dd>
              {text(scope?.name) ?? "—"}
              {text(scope?.version) ? ` ${String(scope?.version)}` : ""}
            </dd>
            <dt className="text-muted-foreground">Span ID</dt>
            <dd className="overflow-x-auto font-mono">{span.spanId}</dd>
            <dt className="text-muted-foreground">Parent span</dt>
            <dd className="overflow-x-auto font-mono">
              {span.parentSpanId || "root"}
            </dd>
          </dl>
          {groups.length === 0 && !span.aiKind ? (
            <p className="border-t border-border px-4 py-4 text-[9px] text-muted-foreground">
              No additional attributes on this span.
            </p>
          ) : null}
          {groups.length > 0
            ? groups.map((group) => (
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
              ))
            : null}
        </>
      )}
    </aside>
  );
};

const TraceWaterfall = ({ detail }: { detail: ServiceTraceDetail }) => {
  const [selectedID, setSelectedID] = useState(detail.spans[0]?.spanId ?? "");
  const depths = useMemo(() => spanDepths(detail.spans), [detail.spans]);
  const bounds = traceBounds(detail.spans);
  const selected =
    detail.spans.find((span) => span.spanId === selectedID) ?? detail.spans[0];

  return (
    <div className="grid border-b border-border lg:grid-cols-[minmax(0,1fr)_minmax(24rem,0.45fr)]">
      <section className="min-w-0">
        <div className="overflow-x-auto">
          <div className="min-w-[760px]">
            <div className="grid h-10 grid-cols-[minmax(15rem,0.44fr)_minmax(20rem,1fr)_6rem] items-center border-b border-border px-4 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
              <span>Service / span</span>
              <span className="grid grid-cols-5 text-center tracking-normal normal-case">
                <span>0</span>
                <span>25%</span>
                <span>50%</span>
                <span>75%</span>
                <span>100%</span>
              </span>
              <span className="text-right">Duration</span>
            </div>
            {detail.spans.map((span) => {
              const left =
                Number(
                  ((integer(span.startTimeUnixNano) - bounds.start) * 10_000n) /
                    bounds.duration
                ) / 100;
              const width = Math.min(
                100 - left,
                Math.max(
                  0.35,
                  Number(
                    (integer(span.durationNano) * 10_000n) / bounds.duration
                  ) / 100
                )
              );
              const service = otlpAttribute(span.resource, "service.name");
              const operation = text(asRecord(span.span)?.op);
              return (
                <button
                  className={cn(
                    "grid min-h-10 w-full grid-cols-[minmax(15rem,0.44fr)_minmax(20rem,1fr)_6rem] items-center border-b border-border/70 px-4 text-left text-[9px] hover:bg-muted/30",
                    selected?.spanId === span.spanId && "bg-muted/35"
                  )}
                  key={span.spanId}
                  onClick={() => setSelectedID(span.spanId)}
                  type="button"
                >
                  <span
                    className="flex min-w-0 items-center gap-2"
                    style={{
                      paddingLeft: `${(depths.get(span.spanId) ?? 0) * 14}px`,
                    }}
                  >
                    <SpanMarker span={span} />
                    <span className="min-w-0">
                      <span className="block truncate">
                        {span.aiKind ? aiSpanLabel(span) : span.name}
                      </span>
                      <span className="block truncate text-[8px] text-muted-foreground">
                        {span.aiKind
                          ? span.aiOperation || span.aiKind
                          : (service ?? operation ?? spanKind(span.kind))}
                      </span>
                    </span>
                  </span>
                  <span className="relative mr-4 h-5 bg-[linear-gradient(to_right,var(--border)_1px,transparent_1px)] bg-[length:25%_100%]">
                    <span
                      className={cn(
                        "absolute top-1.5 h-2 min-w-px",
                        spanBarColor(span)
                      )}
                      style={{ left: `${left}%`, width: `${width}%` }}
                    />
                  </span>
                  <span className="text-right text-muted-foreground tabular-nums">
                    {formatDuration(span.durationNano)}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      </section>
      {selected ? (
        <TraceAttributes
          key={selected.spanId}
          span={selected}
          traceSpans={detail.spans}
          traceStart={bounds.start}
        />
      ) : null}
    </div>
  );
};

const TraceOverview = ({ detail }: { detail: ServiceTraceDetail }) => {
  const bounds = traceBounds(detail.spans);
  const root =
    detail.spans.find((span) => !span.parentSpanId) ?? detail.spans[0];
  const payload = sentryPayload(detail);
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

export const ServiceTraceDetailView = ({
  detail,
  onBack,
  projectID,
  serviceID,
}: {
  detail: ServiceTraceDetail;
  onBack: () => void;
  projectID: string;
  serviceID: string;
}) => {
  const root =
    detail.spans.find((span) => !span.parentSpanId) ?? detail.spans[0];
  const payload = sentryPayload(detail);
  const relatedReplayID = replayID(payload);
  const loadRecording = useCallback(
    (id: string, signal: AbortSignal) =>
      fetchServiceReplayRecording(projectID, serviceID, id, signal),
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
      <TraceWaterfall detail={detail} />
      {payload ? (
        <div className="px-5 lg:px-7">
          {relatedReplayID ? (
            <RelatedReplay
              appId={serviceID}
              loadRecording={loadRecording}
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
