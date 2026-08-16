import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import type { SortingState } from "@tanstack/react-table";
import {
  AlertTriangle,
  Filter,
  Pause,
  Play,
  RefreshCw,
  Search,
} from "lucide-react";
import { useQueryStates } from "nuqs";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { MetricScope, ResourceLogKind } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { LogExportMenu } from "@/log-export-menu";
import type { LogFieldFilter } from "@/log-field-filter";
import { LogFilterChips, LogFilterPanel } from "@/log-filter-panel";
import { atLeastLogSeverity, logSeverity } from "@/log-severity";
import { logTableColumns } from "@/log-table-columns";
import { TelemetryHistogram } from "@/telemetry-histogram";
import { logQueryParsers } from "@/telemetry-query-state";
import type { LogSort } from "@/telemetry-query-state";
import {
  TelemetryTimeRangePicker,
  telemetryTimeBounds,
} from "@/telemetry-time-range";
import { useTelemetryLogWindow } from "@/use-resource-log-window";
import type { TelemetryLogSource } from "@/use-resource-log-window";

const inTimeRange = (
  timestamp: string,
  bounds: { from?: number; to?: number }
) => {
  const value = new Date(timestamp).getTime();
  return (
    (bounds.from === undefined || value >= bounds.from) &&
    (bounds.to === undefined || value <= bounds.to)
  );
};

const logCorrelation = (
  acceptsTraceContext: boolean,
  traceID: string | null,
  spanID: string | null
) => {
  if (!acceptsTraceContext) {
    return {};
  }
  return {
    spanID: spanID ?? undefined,
    traceID: traceID ?? undefined,
  };
};

const logSourceContext = (source: TelemetryLogSource) => {
  if (source.kind === "resource") {
    return {
      acceptsTraceContext: source.resourceKind === "service",
      exportName: source.resourceID,
      scoped: false,
    };
  }
  if (source.scope.kind === "project") {
    return {
      acceptsTraceContext: true,
      exportName: source.scope.projectID,
      scoped: true,
    };
  }
  return {
    acceptsTraceContext: true,
    exportName: "installation",
    scoped: true,
  };
};

// TanStack Table exposes callback-rich mutable state, so React Compiler must
// leave this component alone; the table owns its own memoization.
// oxlint-disable-next-line react/react-compiler
export const TelemetryLogs = ({
  deploymentID,
  serviceName,
  source,
}: {
  deploymentID?: string;
  serviceName?: (serviceID: string) => string | undefined;
  source: TelemetryLogSource;
}) => {
  "use no memo";
  const [logState, setLogState] = useQueryStates(logQueryParsers);
  const {
    deployment: urlDeploymentID,
    logFields: fieldFilters,
    logLevel: severityFilter,
    logOrder,
    logQuery: appliedQuery,
    logSort,
    logSpan,
    logTrace,
    timeFrom,
    timeRange,
    timeTo,
  } = logState;
  const { acceptsTraceContext, exportName, scoped } = logSourceContext(source);
  const activeDeploymentID = deploymentID ?? urlDeploymentID ?? undefined;
  const activeFieldFilters = fieldFilters;
  const { spanID: activeSpanID, traceID: activeTraceID } = logCorrelation(
    acceptsTraceContext,
    logTrace,
    logSpan
  );
  const [controlsOpen, setControlsOpen] = useState(false);
  const [jumpedLogRowID, setJumpedLogRowID] = useState<string>();
  const sorting = useMemo<SortingState>(
    () => [{ desc: logOrder === "desc", id: logSort }],
    [logOrder, logSort]
  );
  const scrollViewportRef = useRef<HTMLDivElement>(null);
  const loadMoreRef = useRef<HTMLDivElement>(null);
  const {
    error,
    live,
    loading,
    loadingMore,
    loadMore,
    refresh,
    setLive,
    window,
  } = useTelemetryLogWindow({
    contains: appliedQuery,
    deploymentID: activeDeploymentID,
    fieldFilters: activeFieldFilters,
    source,
    spanID: activeSpanID,
    timeFrom,
    timeRange,
    timeTo,
    traceID: activeTraceID,
  });

  useEffect(() => {
    const root = scrollViewportRef.current;
    const target = loadMoreRef.current;
    if (!(root && target && window?.nextCursor)) {
      return;
    }
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          void loadMore();
        }
      },
      { root, rootMargin: "240px 0px" }
    );
    observer.observe(target);
    return () => observer.disconnect();
  }, [loadMore, window?.nextCursor]);

  const records = useMemo(() => window?.records ?? [], [window]);
  const filtered = useMemo(() => {
    const bounds = telemetryTimeBounds({
      from: timeFrom,
      range: timeRange,
      to: timeTo,
    });
    return records.filter(
      (record) =>
        atLeastLogSeverity(record, severityFilter) &&
        inTimeRange(record.timestamp, bounds)
    );
  }, [records, severityFilter, timeFrom, timeRange, timeTo]);
  const histogramBounds = telemetryTimeBounds({
    from: timeFrom,
    range: timeRange,
    to: timeTo,
  });
  const histogramPoints = useMemo(
    () =>
      filtered.map((record, index) => ({
        error: logSeverity(record) === "error",
        id: index.toString(),
        timestamp: new Date(record.timestamp).getTime(),
      })),
    [filtered]
  );

  const addFieldFilter = useCallback(
    (filter: LogFieldFilter) => {
      if (
        activeFieldFilters.some(
          (existing) =>
            existing.path === filter.path &&
            existing.operator === filter.operator &&
            existing.value === filter.value
        )
      ) {
        return;
      }
      void setLogState({ logFields: [...activeFieldFilters, filter] });
    },
    [activeFieldFilters, setLogState]
  );
  const columns = useMemo(
    () => logTableColumns(addFieldFilter, { serviceName, showService: scoped }),
    [addFieldFilter, scoped, serviceName]
  );
  const table = useReactTable({
    columns,
    data: filtered,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    onSortingChange: (updater) => {
      const next = typeof updater === "function" ? updater(sorting) : updater;
      const primary = next[0] ?? { desc: true, id: "timestamp" };
      void setLogState({
        logOrder: primary.desc ? "desc" : "asc",
        logSort: primary.id as LogSort,
      });
    },
    state: { sorting },
  });
  const displayedRecords = table.getRowModel().rows.map((row) => row.original);

  const jumpToLog = (rowID?: string) => {
    if (!rowID) {
      return;
    }
    setJumpedLogRowID(rowID);
    requestAnimationFrame(() => {
      const viewport = scrollViewportRef.current;
      const row = viewport?.querySelector<HTMLTableRowElement>(
        `[data-log-row-id="${rowID}"]`
      );
      if (!(viewport && row)) {
        return;
      }
      viewport.scrollTo({
        behavior: "smooth",
        top: row.offsetTop - viewport.clientHeight / 2 + row.clientHeight / 2,
      });
    });
  };

  const resetFilters = () => {
    void setLogState({
      ...(deploymentID ? {} : { deployment: null }),
      logFields: null,
      logLevel: "info",
      logSpan: null,
      logTrace: null,
    });
  };
  const activeFilters =
    Number(severityFilter !== "info") +
    Number(Boolean(urlDeploymentID && !deploymentID)) +
    Number(Boolean(activeTraceID)) +
    Number(Boolean(activeSpanID)) +
    activeFieldFilters.length;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden border-y border-border bg-background">
      <header className="flex min-h-12 shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2">
        <div className="flex items-baseline gap-2">
          <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Log window
          </span>
          <span className="text-[9px] text-muted-foreground tabular-nums">
            {filtered.length.toLocaleString()} /{" "}
            {records.length.toLocaleString()}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <TelemetryTimeRangePicker
            onChange={(value) =>
              void setLogState({
                timeFrom: value.from,
                timeRange: value.range,
                timeTo: value.to,
              })
            }
            value={{ from: timeFrom, range: timeRange, to: timeTo }}
          />
          <LogExportMenu records={displayedRecords} resourceID={exportName} />
          <Button
            aria-label="Refresh logs"
            onClick={refresh}
            size="icon"
            variant="ghost"
          >
            <RefreshCw className={cn(loading && "animate-spin")} />
          </Button>
          <Button
            aria-pressed={live}
            onClick={() => setLive((value) => !value)}
            size="sm"
            variant={live ? "default" : "outline"}
          >
            {live ? <Pause /> : <Play />}
            {live ? "Live" : "Paused"}
          </Button>
        </div>
      </header>

      {error ? (
        <div className="flex shrink-0 items-center gap-2 border-b border-destructive/40 bg-destructive/5 px-4 py-3 text-[10px] text-destructive">
          <AlertTriangle className="size-3.5" />
          {error}
        </div>
      ) : null}
      <div className="shrink-0">
        <TelemetryHistogram
          ariaLabel="Log volume over time"
          bounds={histogramBounds}
          noun="logs"
          onJumpTo={(point) => jumpToLog(point.id)}
          points={histogramPoints}
        />
      </div>

      <div className="flex min-h-12 shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <form
          className="relative min-w-64 flex-1"
          onSubmit={(event) => {
            event.preventDefault();
            const nextQuery = String(
              new FormData(event.currentTarget).get("query") ?? ""
            ).trim();
            if (nextQuery === appliedQuery) {
              refresh();
            } else {
              void setLogState({ logQuery: nextQuery });
            }
          }}
        >
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="Search logs"
            className="h-8 pl-7 text-[10px]"
            defaultValue={appliedQuery}
            key={appliedQuery}
            maxLength={256}
            name="query"
            placeholder="Search message or structured JSON"
          />
        </form>
        <LogFilterChips
          deploymentID={activeDeploymentID}
          fieldFilters={activeFieldFilters}
          onRemoveDeployment={
            deploymentID
              ? undefined
              : () => void setLogState({ deployment: null })
          }
          onRemoveFieldFilter={(index) =>
            void setLogState({
              logFields: activeFieldFilters.filter(
                (_, candidate) => candidate !== index
              ),
            })
          }
          onRemoveSpan={() => void setLogState({ logSpan: null })}
          onRemoveTrace={() =>
            void setLogState({ logSpan: null, logTrace: null })
          }
          severity={severityFilter}
          spanID={activeSpanID}
          traceID={activeTraceID}
        />
        <Button
          aria-pressed={controlsOpen}
          className="h-8"
          onClick={() => setControlsOpen((value) => !value)}
          size="sm"
          variant={controlsOpen ? "secondary" : "outline"}
        >
          <Filter />
          Filters{activeFilters ? ` · ${activeFilters.toString()}` : ""}
        </Button>
      </div>

      {controlsOpen ? (
        <div className="shrink-0">
          <LogFilterPanel
            fieldFilters={activeFieldFilters}
            onAddFieldFilter={addFieldFilter}
            onReset={resetFilters}
            onSeverityChange={(value) => void setLogState({ logLevel: value })}
            records={records}
            severity={severityFilter}
            structured
          />
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-hidden">
        <div className="h-full min-w-0 overflow-auto" ref={scrollViewportRef}>
          <table className="w-full min-w-[980px] border-collapse font-mono text-[9px]">
            <thead className="sticky top-0 z-10 bg-background">
              {table.getHeaderGroups().map((headerGroup) => (
                <tr key={headerGroup.id}>
                  {headerGroup.headers.map((header) => (
                    <th
                      className="h-10 border-r border-b border-border px-3 text-left font-normal tracking-[0.08em] text-muted-foreground uppercase last:border-r-0"
                      key={header.id}
                      style={{ width: header.getSize() }}
                    >
                      {header.isPlaceholder ? null : (
                        <button
                          className="w-full py-0.5 text-left hover:text-foreground disabled:pointer-events-none"
                          disabled={!header.column.getCanSort()}
                          onClick={header.column.getToggleSortingHandler()}
                          type="button"
                        >
                          {flexRender(
                            header.column.columnDef.header,
                            header.getContext()
                          )}
                        </button>
                      )}
                    </th>
                  ))}
                </tr>
              ))}
            </thead>
            <tbody>
              {table.getRowModel().rows.map((row) => (
                <tr
                  className={cn(
                    "align-top transition-colors hover:bg-muted/25",
                    jumpedLogRowID === row.id && "bg-sky-500/10"
                  )}
                  data-log-row-id={row.id}
                  key={row.id}
                >
                  {row.getVisibleCells().map((cell) => (
                    <td
                      className="border-r border-b border-border/65 px-3 py-2.5 last:border-r-0"
                      key={cell.id}
                    >
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext()
                      )}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {window?.nextCursor ? (
            <div
              className="grid h-10 place-items-center text-[9px] text-muted-foreground"
              ref={loadMoreRef}
            >
              {loadingMore ? "Loading older logs…" : null}
            </div>
          ) : null}
          {table.getRowModel().rows.length === 0 ? (
            <div className="grid min-h-72 place-items-center px-8 text-center">
              <div>
                <p className="text-xs font-medium">
                  {loading ? "Loading logs" : "No matching records"}
                </p>
                <p className="mt-2 text-[9px] text-muted-foreground">
                  Change the query or reset the active facets.
                </p>
              </div>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
};

export const ResourceLogs = ({
  deploymentID,
  kind,
  projectID,
  resourceID,
}: {
  deploymentID?: string;
  kind: ResourceLogKind;
  projectID: string;
  resourceID: string;
}) => {
  const source = useMemo<TelemetryLogSource>(
    () => ({
      kind: "resource",
      projectID,
      resourceID,
      resourceKind: kind,
    }),
    [kind, projectID, resourceID]
  );
  return <TelemetryLogs deploymentID={deploymentID} source={source} />;
};

export const ScopedTelemetryLogs = ({
  scope,
  serviceName,
}: {
  scope: Exclude<MetricScope, { kind: "service" }>;
  serviceName?: (serviceID: string) => string | undefined;
}) => {
  const projectID = scope.kind === "project" ? scope.projectID : undefined;
  const source = useMemo<TelemetryLogSource>(
    () => ({
      kind: "scope",
      scope:
        scope.kind === "project" && projectID
          ? { kind: "project", projectID }
          : { kind: "installation" },
    }),
    [projectID, scope.kind]
  );
  return <TelemetryLogs serviceName={serviceName} source={source} />;
};
