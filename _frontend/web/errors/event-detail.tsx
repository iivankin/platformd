import {
  ArrowDownUp,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  FileSearch,
  Route,
  Rows3,
  TriangleAlert,
} from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { Eyebrow, StatusBadge } from "./common-ui";
import { DetailGrid, DownloadButton } from "./detail-common";
import { asRecord } from "./event-context";
import {
  EventEnvironmentSection,
  EventRequestSection,
} from "./event-context-sections";
import { BreadcrumbsSection, ContextSection } from "./event-sections";
import {
  eventStackGroups,
  relevantStackFrames,
  sourceContextLines,
} from "./event-stack";
import type { StackFrame } from "./event-stack";
import { formatTime, shortId } from "./format";
import { RelatedReplay } from "./related-replay";
import { SyntaxSource } from "./syntax-source";
import type { EventDetail } from "./types";

const optionalString = (value: unknown) =>
  typeof value === "string" && value !== "" ? value : undefined;

export { framesFromStacktrace, sourceContextLines } from "./event-stack";

const SourceContext = ({ frame }: { frame: StackFrame }) => {
  const lines = sourceContextLines(frame);
  if (lines.length === 0) {
    return null;
  }
  return (
    <div
      aria-label={`Source context for ${frame.filename ?? "stack frame"}`}
      className="border-t border-border"
    >
      <SyntaxSource filename={frame.filename} lines={lines} />
    </div>
  );
};

const selectedStackFrame = (
  selected: null | string | undefined,
  displayed: ReadonlySet<string>,
  fallback?: string
) => {
  if (selected === null) {
    return;
  }
  return selected && displayed.has(selected) ? selected : fallback;
};

const StackTrace = ({ detail }: { detail: EventDetail }) => {
  const groups = useMemo(() => eventStackGroups(detail), [detail]);
  const hasRelevantFrames = groups.some((group) =>
    group.frames.some((frame) => frame.inApp === true)
  );
  const [showFullStack, setShowFullStack] = useState(false);
  const [newestFirst, setNewestFirst] = useState(true);
  const [selectedFrameKey, setSelectedFrameKey] = useState<
    null | string | undefined
  >();
  const relevantOnly = hasRelevantFrames && !showFullStack;
  const displayedGroups = useMemo(
    () =>
      groups.flatMap((group) => {
        const relevant = relevantOnly
          ? relevantStackFrames(group.frames)
          : group.frames;
        const frames = newestFirst ? relevant.toReversed() : relevant;
        return frames.length > 0 ? [{ ...group, frames }] : [];
      }),
    [groups, newestFirst, relevantOnly]
  );
  const frameCount = groups.reduce(
    (total, group) => total + group.frames.length,
    0
  );
  if (frameCount === 0) {
    return null;
  }
  const firstFrameKey = displayedGroups[0]?.frames[0]
    ? `0:${displayedGroups[0].label}:${displayedGroups[0].frames[0].filename ?? "unknown"}:${displayedGroups[0].frames[0].lineno ?? 0}:0`
    : undefined;
  const displayedFrameKeys = new Set(
    displayedGroups.flatMap((group, groupIndex) =>
      group.frames.map(
        (frame, index) =>
          `${groupIndex}:${group.label}:${frame.filename ?? "unknown"}:${frame.lineno ?? 0}:${index}`
      )
    )
  );
  const activeFrameKey = selectedStackFrame(
    selectedFrameKey,
    displayedFrameKeys,
    firstFrameKey
  );
  return (
    <section className="border-b border-border py-6">
      <div className="mb-4 flex items-center justify-between gap-4">
        <div>
          <Eyebrow>Stack trace</Eyebrow>
          <p className="mt-1.5 text-[10px] text-muted-foreground">
            {frameCount.toLocaleString()} frame{frameCount === 1 ? "" : "s"}
            {groups.length > 1 ? ` across ${groups.length} exceptions` : ""}
          </p>
        </div>
        <div className="flex items-center gap-1">
          {hasRelevantFrames ? (
            <Button
              aria-pressed={relevantOnly}
              onClick={() => setShowFullStack((value) => !value)}
              size="sm"
              variant="ghost"
            >
              <Rows3 /> {relevantOnly ? "Relevant" : "Full stack"}
            </Button>
          ) : null}
          <Button
            aria-pressed={newestFirst}
            onClick={() => setNewestFirst((value) => !value)}
            size="sm"
            variant="ghost"
          >
            <ArrowDownUp /> {newestFirst ? "Newest first" : "Oldest first"}
          </Button>
          <StatusBadge value={detail.symbolication ? "symbolicated" : "raw"} />
        </div>
      </div>
      <div className="border-y border-border">
        {displayedGroups.map((group, groupIndex) => (
          <div
            className="border-b border-border last:border-b-0"
            key={`${groupIndex}:${group.label}`}
          >
            <div className="flex flex-wrap items-center gap-2 bg-muted/25 px-3 py-2 text-[9px] [overflow-wrap:anywhere]">
              <span className="font-medium">{group.label}</span>
              {group.relationship ? (
                <span className="text-muted-foreground">
                  {group.relationship}
                </span>
              ) : null}
              {group.mechanism?.type ? (
                <span
                  className="inline-flex items-center gap-1 border border-border px-1.5 py-0.5 text-muted-foreground"
                  title={group.mechanism.description}
                >
                  mechanism · {group.mechanism.type}
                  {group.mechanism.helpLink ? (
                    <a
                      aria-label="Open mechanism documentation"
                      href={group.mechanism.helpLink}
                      onClick={(event) => event.stopPropagation()}
                      rel="noreferrer"
                      target="_blank"
                    >
                      <ExternalLink className="size-2.5" />
                    </a>
                  ) : null}
                </span>
              ) : null}
              {group.mechanism?.handled === undefined ? null : (
                <span className="border border-border px-1.5 py-0.5 text-muted-foreground">
                  handled · {String(group.mechanism.handled)}
                </span>
              )}
              {group.mechanism?.source ? (
                <span className="border border-border px-1.5 py-0.5 text-muted-foreground">
                  source · {group.mechanism.source}
                </span>
              ) : null}
              {group.mechanism?.data.map((item) => (
                <span
                  className="border border-border px-1.5 py-0.5 text-muted-foreground"
                  key={item.name}
                >
                  {item.name} · {item.value}
                </span>
              ))}
            </div>
            <div className="divide-y divide-border">
              {group.frames.map((frame, index) => {
                const frameKey = `${groupIndex}:${group.label}:${frame.filename ?? "unknown"}:${frame.lineno ?? 0}:${index}`;
                const expanded = frameKey === activeFrameKey;
                return (
                  <div className="text-[10px]" key={frameKey}>
                    <button
                      aria-expanded={expanded}
                      className="grid w-full grid-cols-[20px_32px_minmax(0,1fr)_auto] items-center gap-3 px-3 py-3 text-left hover:bg-muted/25"
                      onClick={() =>
                        setSelectedFrameKey(expanded ? null : frameKey)
                      }
                      type="button"
                    >
                      {expanded ? (
                        <ChevronDown className="size-3 text-muted-foreground" />
                      ) : (
                        <ChevronRight className="size-3 text-muted-foreground" />
                      )}
                      <span className="grid size-7 place-items-center border border-border text-[9px] text-muted-foreground">
                        {index + 1}
                      </span>
                      <div className="min-w-0">
                        <p className="overflow-hidden font-medium text-ellipsis whitespace-nowrap">
                          {frame.functionName ?? "<anonymous>"}
                        </p>
                        <code className="mt-1 block overflow-hidden text-[9px] text-ellipsis whitespace-nowrap text-muted-foreground">
                          {[frame.module, frame.filename]
                            .filter(Boolean)
                            .join(" · ") || "unknown source"}
                        </code>
                      </div>
                      <span className="flex items-center gap-2">
                        {frame.inApp ? <StatusBadge value="in app" /> : null}
                        <code className="text-[9px] text-muted-foreground">
                          {frame.lineno ?? "—"}:{frame.colno ?? "—"}
                        </code>
                      </span>
                    </button>
                    {expanded ? <SourceContext frame={frame} /> : null}
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
};

const SourceMapDiagnostics = ({ detail }: { detail: EventDetail }) => {
  const platform = detail.event.platform?.toLowerCase() ?? "";
  if (!(platform.includes("javascript") || platform.includes("node"))) {
    return null;
  }
  const payload = asRecord(detail.symbolication?.payload);
  const errors = Array.isArray(payload?.errors)
    ? payload.errors.flatMap((entry) =>
        asRecord(entry) ? [asRecord(entry)] : []
      )
    : [];
  const frames = eventStackGroups(detail).flatMap((group) => group.frames);
  const resolved = frames.filter((frame) => frame.symbolicated).length;
  const { dist, release } = detail.event;
  let summary = "No original frame mapping was available";
  if (resolved > 0) {
    summary = `${resolved.toLocaleString()} original frame${resolved === 1 ? "" : "s"} resolved`;
  } else if (errors.length > 0) {
    summary = "Some original sources could not be resolved";
  }
  return (
    <section className="border-b border-border py-5">
      <div className="flex items-start justify-between gap-6 max-sm:flex-col">
        <div>
          <Eyebrow>Source maps</Eyebrow>
          <p className="mt-2 flex items-center gap-2 text-[10px]">
            {errors.length > 0 ? (
              <TriangleAlert className="size-3.5 text-amber-500" />
            ) : (
              <FileSearch className="size-3.5 text-emerald-500" />
            )}
            {summary}
          </p>
        </div>
        <div className="text-right text-[9px] text-muted-foreground max-sm:text-left">
          <p>{release ? `release ${release}` : "release not set"}</p>
          <p className="mt-1">{dist ? `dist ${dist}` : "dist not set"}</p>
        </div>
      </div>
      {errors.length > 0 ? (
        <div className="mt-4 divide-y divide-border border-y border-border">
          {errors.map((error, index) => (
            <div
              className="grid gap-1 py-2.5 text-[9px]"
              key={`${error?.type}:${index}`}
            >
              <span className="font-medium">
                {error?.type === "missing_source"
                  ? "Source is missing from the uploaded bundle"
                  : "Source map could not be processed"}
              </span>
              <code className="truncate text-muted-foreground">
                {[error?.abs_path, error?.message]
                  .filter((value): value is string => typeof value === "string")
                  .join(" · ")}
              </code>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
};

interface TraceContext {
  operation?: string;
  spanId?: string;
  status?: string;
  traceId: string;
}

const traceContext = (payload: unknown): TraceContext | undefined => {
  const trace = asRecord(asRecord(asRecord(payload)?.contexts)?.trace);
  const traceId = optionalString(trace?.trace_id);
  return traceId
    ? {
        operation: optionalString(trace?.op),
        spanId: optionalString(trace?.span_id),
        status: optionalString(trace?.status),
        traceId,
      }
    : undefined;
};

const EventHighlights = ({
  detail,
  onOpenTrace,
}: {
  detail: EventDetail;
  onOpenTrace?: (traceId: string) => void;
}) => {
  const payload = asRecord(detail.event.payload);
  const user = asRecord(payload?.user);
  const trace = traceContext(payload);
  const values = [
    [
      "Transaction",
      optionalString(payload?.transaction) ?? detail.event.transaction,
    ],
    ["Release", optionalString(payload?.release) ?? detail.event.release],
    [
      "Environment",
      optionalString(payload?.environment) ?? detail.event.environment,
    ],
    [
      "User",
      optionalString(user?.email) ??
        optionalString(user?.username) ??
        optionalString(user?.id),
    ],
    [
      "SDK",
      [detail.event.sdk_name, detail.event.sdk_version]
        .filter(Boolean)
        .join(" "),
    ],
  ].filter((entry): entry is [string, string] => Boolean(entry[1]));
  if (values.length === 0 && !trace) {
    return null;
  }
  return (
    <section className="border-b border-border py-5">
      <Eyebrow>Highlights</Eyebrow>
      <div className="mt-3 grid grid-cols-3 border-t border-l border-border max-lg:grid-cols-2 max-sm:grid-cols-1">
        {values.map(([label, value]) => (
          <div
            className="min-w-0 border-r border-b border-border px-3 py-3"
            key={label}
          >
            <p className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
              {label}
            </p>
            <p className="mt-1.5 truncate text-[9px]" title={value}>
              {value}
            </p>
          </div>
        ))}
        {trace ? (
          <button
            className={cn(
              "min-w-0 border-r border-b border-border px-3 py-3 text-left",
              onOpenTrace && "hover:bg-muted/30"
            )}
            disabled={!onOpenTrace}
            onClick={() => onOpenTrace?.(trace.traceId)}
            type="button"
          >
            <p className="flex items-center gap-1.5 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
              <Route className="size-3" /> Trace
            </p>
            <p
              className="mt-1.5 truncate font-mono text-[9px]"
              title={trace.traceId}
            >
              {trace.traceId}
            </p>
          </button>
        ) : null}
      </div>
    </section>
  );
};

const RelatedTrace = ({
  onOpenTrace,
  payload,
}: {
  onOpenTrace?: (traceId: string) => void;
  payload: unknown;
}) => {
  const trace = traceContext(payload);
  if (!trace) {
    return null;
  }
  return (
    <section className="border-b border-border py-6">
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <Eyebrow>Trace</Eyebrow>
          <p
            className="mt-2 truncate font-mono text-[10px]"
            title={trace.traceId}
          >
            {trace.traceId}
          </p>
          <p className="mt-1 text-[9px] text-muted-foreground">
            {[trace.operation, trace.status, trace.spanId]
              .filter(Boolean)
              .join(" · ")}
          </p>
        </div>
        {onOpenTrace ? (
          <Button onClick={() => onOpenTrace(trace.traceId)} variant="outline">
            <Route /> Open trace
          </Button>
        ) : null}
      </div>
    </section>
  );
};

export const EventEvidence = ({
  appId,
  detail,
  label = "Event evidence",
  notify,
  onOpenTrace,
}: {
  appId: string;
  detail: EventDetail;
  label?: string;
  notify: (message: string) => void;
  onOpenTrace?: (traceId: string) => void;
}) => {
  const item = detail.event;
  return (
    <div className="px-5 lg:px-7">
      <section className="border-b border-border py-6">
        <div className="flex items-start justify-between gap-6">
          <div className="min-w-0">
            <Eyebrow>{label}</Eyebrow>
            <p className="mt-2 overflow-hidden text-xs font-medium text-ellipsis whitespace-nowrap">
              {item.event_id ?? "Unknown event"}
            </p>
            <p className="mt-1 text-[9px] text-muted-foreground">
              Received {formatTime(item.received_at)}
            </p>
          </div>
          {item.content_id ? (
            <DownloadButton
              appId={appId}
              contentId={item.content_id}
              filename={`${item.event_id ?? "event"}.json`}
              onError={notify}
            />
          ) : null}
        </div>
      </section>
      <EventHighlights detail={detail} onOpenTrace={onOpenTrace} />
      <StackTrace detail={detail} />
      <SourceMapDiagnostics detail={detail} />
      {item.replay_id ? (
        <RelatedReplay
          appId={appId}
          onOpenTrace={onOpenTrace}
          replayId={item.replay_id}
        />
      ) : null}
      <BreadcrumbsSection payload={item.payload} />
      <RelatedTrace onOpenTrace={onOpenTrace} payload={item.payload} />
      <EventEnvironmentSection payload={item.payload} />
      <EventRequestSection payload={item.payload} />
      <ContextSection payload={item.payload} />
      {item.payload === undefined ? (
        <section className="py-6 text-[10px] text-muted-foreground">
          The indexed event is available above. Download the raw item to inspect
          payload fields that exceeded the inline indexing limit.
        </section>
      ) : null}
    </div>
  );
};

export const EventDetailView = ({
  appId,
  detail,
  notify,
  onOpenTrace,
}: {
  appId: string;
  detail: EventDetail;
  notify: (message: string) => void;
  onOpenTrace?: (traceId: string) => void;
}) => {
  const item = detail.event;
  return (
    <>
      <section className="border-b border-border px-5 py-6 lg:px-7">
        <div className="max-w-5xl">
          <div className="flex items-center gap-2">
            <StatusBadge value={item.level} />
            <span className="text-[9px] text-muted-foreground">
              {item.platform ?? "other"}
            </span>
          </div>
          <h1 className="mt-3 text-xl font-medium tracking-[-0.035em]">
            {item.title ?? item.message ?? "Event"}
          </h1>
          <div className="mt-5 max-w-3xl">
            <DetailGrid
              rows={[
                ["Event ID", item.event_id],
                ["Issue", shortId(item.issue_id, 24)],
                [
                  "SDK",
                  [item.sdk_name, item.sdk_version].filter(Boolean).join(" "),
                ],
                ["Environment", item.environment],
                ["Received", formatTime(item.received_at)],
              ]}
            />
          </div>
        </div>
      </section>
      <EventEvidence
        appId={appId}
        detail={detail}
        notify={notify}
        onOpenTrace={onOpenTrace}
      />
    </>
  );
};
