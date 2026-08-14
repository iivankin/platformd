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

import type { ResourceLogKind } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
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
import { useResourceLogWindow } from "@/use-resource-log-window";

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

// TanStack Table exposes callback-rich mutable state, so React Compiler must
// leave this component alone; the table owns its own memoization.
// oxlint-disable-next-line react/react-compiler
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
  "use no memo";
  const [logState, setLogState] = useQueryStates(logQueryParsers);
  const {
    deployment: urlDeploymentID,
    logFields: fieldFilters,
    logLevel: severityFilter,
    logOrder,
    logQuery: appliedQuery,
    logSort,
    timeFrom,
    timeRange,
    timeTo,
  } = logState;
  const activeDeploymentID = deploymentID ?? urlDeploymentID ?? undefined;
  const activeFieldFilters = fieldFilters;
  const [controlsOpen, setControlsOpen] = useState(false);
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
  } = useResourceLogWindow({
    contains: appliedQuery,
    deploymentID: activeDeploymentID,
    fieldFilters: activeFieldFilters,
    kind,
    projectID,
    resourceID,
    timeFrom,
    timeRange,
    timeTo,
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
      filtered.map((record) => ({
        error: logSeverity(record) === "error",
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
    () => logTableColumns(addFieldFilter),
    [addFieldFilter]
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

  const resetFilters = () => {
    void setLogState({
      ...(deploymentID ? {} : { deployment: null }),
      logFields: null,
      logLevel: "info",
    });
  };
  const activeFilters =
    Number(severityFilter !== "info") +
    Number(Boolean(urlDeploymentID && !deploymentID)) +
    activeFieldFilters.length;

  return (
    <div className="min-h-[34rem] border-y border-border bg-background">
      <header className="flex min-h-12 flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2">
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
        <div className="flex items-center gap-2 border-b border-destructive/40 bg-destructive/5 px-4 py-3 text-[10px] text-destructive">
          <AlertTriangle className="size-3.5" />
          {error}
        </div>
      ) : null}
      <TelemetryHistogram
        ariaLabel="Log volume over time"
        bounds={histogramBounds}
        noun="logs"
        onSelectRange={(from, to) => {
          setLive(false);
          void setLogState({
            timeFrom: from,
            timeRange: "custom",
            timeTo: to,
          });
        }}
        points={histogramPoints}
      />

      <div className="flex min-h-12 flex-wrap items-center gap-2 border-b border-border px-3 py-2">
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
          severity={severityFilter}
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
        <LogFilterPanel
          fieldFilters={activeFieldFilters}
          onAddFieldFilter={addFieldFilter}
          onReset={resetFilters}
          onSeverityChange={(value) => void setLogState({ logLevel: value })}
          records={records}
          severity={severityFilter}
          structured
        />
      ) : null}

      <div className="min-h-[31rem]">
        <div
          className={cn(
            "min-h-[31rem] min-w-0 overflow-auto",
            controlsOpen ? "h-[calc(100vh-21rem)]" : "h-[calc(100vh-17.5rem)]"
          )}
          ref={scrollViewportRef}
        >
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
                <tr className="align-top hover:bg-muted/25" key={row.id}>
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
