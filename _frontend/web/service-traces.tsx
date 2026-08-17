import {
  ArrowLeft,
  Bot,
  ChevronRight,
  LoaderCircle,
  RefreshCw,
  Search,
  Waypoints,
} from "lucide-react";
import { useQueryStates } from "nuqs";
import { useDeferredValue, useEffect, useMemo, useState } from "react";

import { calculateAiPrice, formatAiPrice } from "@/ai-price";
import { fetchTelemetryTrace, fetchTelemetryTraces } from "@/api";
import type {
  MetricScope,
  ServiceTraceDetail,
  ServiceTraceSummary,
} from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { ServiceTraceDetailView } from "@/service-trace-detail";
import { TelemetryHistogram } from "@/telemetry-histogram";
import { traceQueryParsers } from "@/telemetry-query-state";
import {
  TelemetryTimeRangePicker,
  telemetryTimeBounds,
} from "@/telemetry-time-range";

const integer = (value: string) => globalThis.BigInt(value);

const nanosToMilliseconds = (value: string) =>
  Number(integer(value)) / 1_000_000;

const nanosToDate = (value: string) =>
  new Date(Number(integer(value) / 1_000_000n));

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

const shortID = (value: string) => `${value.slice(0, 8)}…${value.slice(-6)}`;

const TraceUsage = ({ trace }: { trace: ServiceTraceSummary }) => {
  if (!trace.isAi) {
    return <span className="text-muted-foreground">—</span>;
  }
  const tokens = (trace.aiInputTokens ?? 0) + (trace.aiOutputTokens ?? 0);
  const price = calculateAiPrice({
    actual: trace.aiCostUsd,
    model: trace.aiModel,
    provider: trace.aiProvider,
    timestamp: nanosToDate(trace.startedAtUnixNano),
    usage: {
      cacheReadTokens: trace.aiCacheReadTokens,
      cacheWriteTokens: trace.aiCacheWriteTokens,
      inputTokens: trace.aiInputTokens,
      outputTokens: trace.aiOutputTokens,
    },
  });
  return (
    <span className="block">
      <span className="block">
        {tokens.toLocaleString()} tok{price ? ` · ${formatAiPrice(price)}` : ""}
      </span>
      <span className="mt-1 block text-[8px] text-muted-foreground">
        {trace.aiCacheReadTokens
          ? `${trace.aiCacheReadTokens.toLocaleString()} cached`
          : "no cache"}
        {trace.aiTokensPerSecond
          ? ` · ${trace.aiTokensPerSecond.toFixed(1)} tok/s`
          : ""}
      </span>
    </span>
  );
};

const aiTraceContext = (trace: ServiceTraceSummary) => {
  let run = "";
  if (trace.aiAgentRunCount === 1) {
    run = trace.aiAgent || "1 agent run";
  } else if (trace.aiAgentRunCount > 1) {
    run = `${trace.aiAgentRunCount.toString()} agent runs`;
  }
  const model = [trace.aiProvider, trace.aiModel].filter(Boolean).join(" · ");
  return [run, model].filter(Boolean).join(" · ");
};

export const TelemetryTraces = ({
  onOpenError,
  onOpenLogs,
  scope,
  serviceName,
}: {
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  scope: MetricScope;
  serviceName?: (serviceID: string) => string | undefined;
}) => {
  const [traceState, setTraceState] = useQueryStates(traceQueryParsers);
  const {
    timeFrom,
    timeRange,
    timeTo,
    trace: selectedTrace,
    traceQuery: query,
    traceSort: sort,
    traceStatus: status,
  } = traceState;
  const traceID = selectedTrace ?? "";
  const deferredQuery = useDeferredValue(query);
  const [traces, setTraces] = useState<ServiceTraceSummary[]>([]);
  const [detail, setDetail] = useState<ServiceTraceDetail>();
  const [listLoading, setListLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [listError, setListError] = useState("");
  const [detailError, setDetailError] = useState<{
    message: string;
    traceID: string;
  }>();
  const [revision, setRevision] = useState(0);
  const [jumpedTraceID, setJumpedTraceID] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      setListLoading(true);
      try {
        const bounds = telemetryTimeBounds({
          from: timeFrom,
          range: timeRange,
          to: timeTo,
        });
        const nextTraces = await fetchTelemetryTraces(
          scope,
          controller.signal,
          globalThis.fetch,
          { ...bounds, query: deferredQuery, sort, status }
        );
        setTraces(nextTraces);
        setListError("");
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setListError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load traces"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setListLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [
    revision,
    deferredQuery,
    scope,
    sort,
    status,
    timeFrom,
    timeRange,
    timeTo,
  ]);

  useEffect(() => {
    if (!traceID) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      setDetailLoading(true);
      setDetailError(undefined);
      try {
        // OTEL-first: detail always returns every segment for this trace_id.
        setDetail(
          await fetchTelemetryTrace(
            scope,
            traceID,
            controller.signal,
            globalThis.fetch
          )
        );
        setDetailError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setDetailError({
            message:
              loadError instanceof Error
                ? loadError.message
                : "Unable to load trace",
            traceID,
          });
        }
      } finally {
        if (!controller.signal.aborted) {
          setDetailLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [scope, traceID]);

  const closeTrace = () => {
    void setTraceState(
      { trace: null, traceSegment: null },
      { history: "push" }
    );
  };
  const histogramBounds = telemetryTimeBounds({
    from: timeFrom,
    range: timeRange,
    to: timeTo,
  });
  const histogramPoints = useMemo(
    () =>
      traces.map((trace) => ({
        error: trace.errorSpanCount > 0,
        id: trace.traceId,
        timestamp: Number(integer(trace.startedAtUnixNano) / 1_000_000n),
      })),
    [traces]
  );

  const jumpToTrace = (identity?: string) => {
    if (!identity) {
      return;
    }
    setJumpedTraceID(identity);
    requestAnimationFrame(() => {
      document
        .querySelector<HTMLElement>(`#${CSS.escape(`trace-row-${identity}`)}`)
        ?.scrollIntoView({ behavior: "smooth", block: "center" });
    });
  };

  if (traceID) {
    const currentDetail = detail?.traceId === traceID ? detail : undefined;
    const currentDetailError =
      detailError?.traceID === traceID ? detailError.message : "";
    if (currentDetail) {
      return (
        <ServiceTraceDetailView
          detail={currentDetail}
          key={currentDetail.traceId}
          onBack={closeTrace}
          onOpenError={onOpenError}
          onOpenLogs={onOpenLogs}
          onOpenTrace={(nextTraceID, nextSegmentID) =>
            void setTraceState(
              {
                trace: nextTraceID,
                traceSegment: nextSegmentID ?? null,
              },
              { history: "push" }
            )
          }
          scope={scope}
        />
      );
    }
    return (
      <div>
        <header className="flex items-center gap-3 border-b border-border px-4 py-4 lg:px-6">
          <Button onClick={closeTrace} size="sm" variant="ghost">
            <ArrowLeft /> Traces
          </Button>
          <code className="text-[9px] text-muted-foreground">{traceID}</code>
        </header>
        {currentDetailError ? (
          <p className="border-b border-destructive/35 bg-destructive/5 px-4 py-3 text-[10px] text-destructive">
            {currentDetailError}
          </p>
        ) : null}
        <div className="grid min-h-96 place-items-center text-muted-foreground">
          {detailLoading || !currentDetailError ? (
            <LoaderCircle className="size-5 animate-spin" />
          ) : (
            <div className="text-center">
              <Waypoints className="mx-auto size-5" />
              <p className="mt-4 text-xs font-medium">Trace unavailable</p>
            </div>
          )}
        </div>
      </div>
    );
  }

  return (
    <div>
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <div className="relative min-w-72 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="Search traces"
            className="h-8 pl-7 text-[10px]"
            onChange={(event) =>
              void setTraceState({ traceQuery: event.target.value })
            }
            placeholder="Search name or trace ID · status:error duration:>500ms"
            maxLength={256}
            type="search"
            value={query}
          />
        </div>
        <TelemetryTimeRangePicker
          onChange={(value) =>
            void setTraceState({
              timeFrom: value.from,
              timeRange: value.range,
              timeTo: value.to,
            })
          }
          value={{ from: timeFrom, range: timeRange, to: timeTo }}
        />
        <Select
          items={[
            { label: "Any status", value: "all" },
            { label: "Errors", value: "error" },
            { label: "Successful", value: "ok" },
          ]}
          onValueChange={(value) =>
            void setTraceState({
              traceStatus: value as typeof traceState.traceStatus,
            })
          }
          value={status}
        >
          <SelectTrigger aria-label="Trace status" className="h-8 text-[9px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">Any status</SelectItem>
            <SelectItem value="error">Errors</SelectItem>
            <SelectItem value="ok">Successful</SelectItem>
          </SelectContent>
        </Select>
        <Select
          items={[
            { label: "Latest", value: "latest" },
            { label: "Slowest", value: "slowest" },
            { label: "Most spans", value: "spans" },
          ]}
          onValueChange={(value) =>
            void setTraceState({
              traceSort: value as typeof traceState.traceSort,
            })
          }
          value={sort}
        >
          <SelectTrigger aria-label="Trace sort" className="h-8 text-[9px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="latest">Latest</SelectItem>
            <SelectItem value="slowest">Slowest</SelectItem>
            <SelectItem value="spans">Most spans</SelectItem>
          </SelectContent>
        </Select>
        <span className="px-2 text-[9px] text-muted-foreground tabular-nums">
          {traces.length}
        </span>
        <Button
          aria-label="Refresh traces"
          onClick={() => setRevision((value) => value + 1)}
          size="icon"
          variant="ghost"
        >
          <RefreshCw className={cn(listLoading && "animate-spin")} />
        </Button>
      </header>
      {listError ? (
        <p className="border-b border-destructive/35 bg-destructive/5 px-4 py-3 text-[10px] text-destructive">
          {listError}
        </p>
      ) : null}
      <TelemetryHistogram
        ariaLabel="Trace volume over time"
        bounds={histogramBounds}
        noun="traces"
        onJumpTo={(point) => jumpToTrace(point.id)}
        points={histogramPoints}
      />
      {traces.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] table-fixed border-collapse">
            <thead>
              <tr className="h-10 text-left text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
                <th className="w-[34%] border-b border-border px-4 font-normal">
                  Trace
                </th>
                {scope.kind === "service" ? null : (
                  <th className="border-b border-border px-4 font-normal">
                    Service
                  </th>
                )}
                <th className="border-b border-border px-4 font-normal">
                  Started
                </th>
                <th className="border-b border-border px-4 font-normal">
                  Duration
                </th>
                <th className="border-b border-border px-4 font-normal">
                  Usage
                </th>
                <th className="border-b border-border px-4 font-normal">
                  Calls
                </th>
                <th className="border-b border-border px-4 font-normal">
                  Status
                </th>
                <th className="w-10 border-b border-border">
                  <span className="sr-only">Open</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {traces.map((trace) => {
                const identity = trace.traceId;
                const open = () =>
                  void setTraceState(
                    { trace: trace.traceId, traceSegment: null },
                    { history: "push" }
                  );
                return (
                  <tr
                    className={cn(
                      "cursor-pointer transition-colors hover:bg-muted/30",
                      jumpedTraceID === identity && "bg-sky-500/10"
                    )}
                    id={`trace-row-${identity}`}
                    key={identity}
                    onClick={open}
                  >
                    <td className="border-b border-border/70 px-4 py-3">
                      <button
                        aria-label={`Open trace ${trace.name || trace.traceId}`}
                        className="block max-w-full text-left focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-none"
                        onClick={(event) => {
                          event.stopPropagation();
                          open();
                        }}
                        type="button"
                      >
                        <span className="block truncate text-[10px] font-medium">
                          {trace.isAi ? (
                            <Bot className="mr-1.5 inline size-3 text-violet-500" />
                          ) : null}
                          {trace.name || "Unnamed trace"}
                        </span>
                        <code className="mt-1 block text-[8px] text-muted-foreground">
                          {trace.isAi
                            ? aiTraceContext(trace) || shortID(trace.traceId)
                            : shortID(trace.traceId)}
                        </code>
                      </button>
                    </td>
                    {scope.kind === "service" ? null : (
                      <td className="border-b border-border/70 px-4 py-3 text-[9px] text-muted-foreground">
                        <span
                          className="block max-w-36 truncate"
                          title={trace.serviceId}
                        >
                          {serviceName?.(trace.serviceId) ??
                            shortID(trace.serviceId)}
                        </span>
                      </td>
                    )}
                    <td className="border-b border-border/70 px-4 py-3 text-[9px] text-muted-foreground">
                      {nanosToDate(trace.startedAtUnixNano).toLocaleString()}
                    </td>
                    <td className="border-b border-border/70 px-4 py-3 text-[9px] tabular-nums">
                      {formatDuration(trace.durationNano)}
                    </td>
                    <td className="border-b border-border/70 px-4 py-3 text-[9px] tabular-nums">
                      <TraceUsage trace={trace} />
                    </td>
                    <td className="border-b border-border/70 px-4 py-3 text-[9px] tabular-nums">
                      {trace.isAi
                        ? [
                            trace.aiAgentRunCount > 0
                              ? `${trace.aiAgentRunCount.toString()} agent`
                              : "",
                            `${trace.aiModelCallCount.toString()} model`,
                            `${trace.aiToolCallCount.toString()} tool`,
                          ]
                            .filter(Boolean)
                            .join(" · ")
                        : trace.spanCount}
                    </td>
                    <td className="border-b border-border/70 px-4 py-3 text-[9px]">
                      {trace.errorSpanCount > 0 ? (
                        <span className="text-destructive">
                          {trace.errorSpanCount} error
                          {trace.errorSpanCount === 1 ? "" : "s"}
                        </span>
                      ) : (
                        <span className="text-emerald-600">ok</span>
                      )}
                    </td>
                    <td className="border-b border-border/70">
                      <span className="grid size-9 place-items-center text-muted-foreground">
                        <ChevronRight className="size-3.5" />
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="grid min-h-72 place-items-center px-8 text-center">
          <div>
            {listLoading ? (
              <LoaderCircle className="mx-auto size-5 animate-spin text-muted-foreground" />
            ) : (
              <Waypoints className="mx-auto size-5 text-muted-foreground" />
            )}
            <p className="mt-4 text-xs font-medium">
              {listLoading ? "Loading traces" : "No matching traces"}
            </p>
            <p className="mt-2 text-[9px] text-muted-foreground">
              Change the query or time range to inspect another trace window.
            </p>
          </div>
        </div>
      )}
    </div>
  );
};

export const ServiceTraces = ({
  onOpenError,
  onOpenLogs,
  projectID,
  serviceID,
}: {
  onOpenError?: (issueID: string, eventID?: string) => void;
  onOpenLogs?: (traceID: string, spanID?: string) => void;
  projectID: string;
  serviceID: string;
}) => {
  const scope = useMemo<MetricScope>(
    () => ({ kind: "service", projectID, serviceID }),
    [projectID, serviceID]
  );
  return (
    <TelemetryTraces
      onOpenError={onOpenError}
      onOpenLogs={onOpenLogs}
      scope={scope}
    />
  );
};
