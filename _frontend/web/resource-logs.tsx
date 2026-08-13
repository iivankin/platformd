import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import type { ColumnDef, SortingState } from "@tanstack/react-table";
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  Braces,
  Filter,
  Pause,
  Play,
  RefreshCw,
  RotateCcw,
  Search,
} from "lucide-react";
import { useQueryStates } from "nuqs";
import { useCallback, useEffect, useMemo, useState } from "react";

import { fetchResourceLogs } from "@/api";
import type { LogRecord, LogWindow, ResourceLogKind } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { logQueryParsers } from "@/telemetry-query-state";
import type { LogSort } from "@/telemetry-query-state";
import {
  TelemetryTimeRangePicker,
  telemetryTimeBounds,
} from "@/telemetry-time-range";

const refreshIntervalMilliseconds = 2000;

type Severity = (typeof logQueryParsers.logLevel)["defaultValue"];

const severity = (record: LogRecord): Exclude<Severity, "all"> => {
  const value = record.severityText?.toLowerCase() ?? "";
  const number = record.severityNumber ?? 0;
  if (value.includes("fatal") || value.includes("error") || number >= 17) {
    return "error";
  }
  if (value.includes("warn") || number >= 13) {
    return "warn";
  }
  if (value.includes("info") || number >= 9) {
    return "info";
  }
  if (value.includes("debug") || value.includes("trace") || number > 0) {
    return "debug";
  }
  return "unset";
};

const severityClass: Record<Exclude<Severity, "all">, string> = {
  debug: "text-violet-600 dark:text-violet-300",
  error: "text-rose-600 dark:text-rose-300",
  info: "text-sky-600 dark:text-sky-300",
  unset: "text-muted-foreground",
  warn: "text-amber-600 dark:text-amber-300",
};

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

const shortID = (value: string) =>
  value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;

const toolbarColumns = (controlsOpen: boolean) =>
  controlsOpen
    ? "grid-cols-[13rem_minmax(16rem,1fr)_auto]"
    : "grid-cols-[auto_minmax(16rem,1fr)_auto]";

const toolbarFilterCell = (controlsOpen: boolean) =>
  controlsOpen ? "border-r border-border max-lg:border-r-0" : "";

const SortHeader = ({
  label,
  sorted,
}: {
  label: string;
  sorted: false | "asc" | "desc";
}) => {
  let icon = <ArrowUpDown className="size-3 opacity-45" />;
  if (sorted === "asc") {
    icon = <ArrowUp className="size-3" />;
  } else if (sorted === "desc") {
    icon = <ArrowDown className="size-3" />;
  }
  return (
    <span className="flex items-center gap-1.5">
      {label}
      {icon}
    </span>
  );
};

const FilterChoice = ({
  active,
  count,
  label,
  onClick,
}: {
  active: boolean;
  count?: number;
  label: string;
  onClick: () => void;
}) => (
  <button
    className={cn(
      "flex h-7 w-full items-center justify-between px-2 text-left text-[9px] hover:bg-muted/40",
      active && "bg-muted text-foreground"
    )}
    onClick={onClick}
    type="button"
  >
    <span>{label}</span>
    {count === undefined ? null : (
      <span className="text-[8px] text-muted-foreground tabular-nums">
        {count}
      </span>
    )}
  </button>
);

const logColumns: ColumnDef<LogRecord>[] = [
  {
    accessorFn: (record) => new Date(record.timestamp).getTime(),
    cell: ({ row }) => (
      <time className="whitespace-nowrap text-muted-foreground tabular-nums">
        {new Date(row.original.timestamp).toLocaleString()}
      </time>
    ),
    header: ({ column }) => (
      <SortHeader label="Timestamp" sorted={column.getIsSorted()} />
    ),
    id: "timestamp",
    size: 176,
  },
  {
    accessorFn: (record) => severity(record),
    cell: ({ row }) => {
      const level = severity(row.original);
      return (
        <span className={cn("uppercase", severityClass[level])}>
          {row.original.severityText || level}
        </span>
      );
    },
    header: ({ column }) => (
      <SortHeader label="Level" sorted={column.getIsSorted()} />
    ),
    id: "severity",
    size: 92,
  },
  {
    accessorKey: "deploymentId",
    cell: ({ getValue }) => {
      const value = String(getValue());
      return (
        <code className="block max-w-36 truncate text-muted-foreground">
          {value ? shortID(value) : "—"}
        </code>
      );
    },
    header: ({ column }) => (
      <SortHeader label="Deployment" sorted={column.getIsSorted()} />
    ),
    size: 150,
  },
  {
    accessorKey: "text",
    cell: ({ row }) => (
      <div className="flex min-w-0 items-start gap-2">
        {row.original.traceId ? (
          <Braces
            aria-label="Correlated with a trace"
            className="mt-0.5 size-3 shrink-0 text-sky-500"
          />
        ) : null}
        <pre className="min-w-0 break-words whitespace-pre-wrap text-foreground">
          {row.original.text}
          {row.original.partial ? (
            <span className="ml-2 text-amber-500">[partial]</span>
          ) : null}
        </pre>
      </div>
    ),
    header: ({ column }) => (
      <SortHeader label="Message" sorted={column.getIsSorted()} />
    ),
    size: 520,
  },
  {
    accessorKey: "traceId",
    cell: ({ getValue }) => {
      const value = String(getValue() ?? "");
      if (value) {
        return (
          <code className="text-sky-600 dark:text-sky-300">
            {shortID(value)}
          </code>
        );
      }
      return <span className="text-muted-foreground/45">—</span>;
    },
    header: ({ column }) => (
      <SortHeader label="Trace" sorted={column.getIsSorted()} />
    ),
    size: 150,
  },
];

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
    logLevel: severityFilter,
    logOrder,
    logQuery: appliedQuery,
    logSort,
    timeFrom,
    timeRange,
    timeTo,
    withTrace: correlatedOnly,
  } = logState;
  const activeDeploymentID = deploymentID ?? urlDeploymentID ?? undefined;
  const [controlsOpen, setControlsOpen] = useState(true);
  const [live, setLive] = useState(true);
  const sorting = useMemo<SortingState>(
    () => [{ desc: logOrder === "desc", id: logSort }],
    [logOrder, logSort]
  );
  const [window, setWindow] = useState<LogWindow>();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const refresh = useCallback(() => {
    setRefreshVersion((value) => value + 1);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        setLoading(true);
        const bounds = telemetryTimeBounds({
          from: timeFrom,
          range: timeRange,
          to: timeTo,
        });
        setWindow(
          await fetchResourceLogs(
            projectID,
            kind,
            resourceID,
            {
              contains: appliedQuery || undefined,
              deploymentId: activeDeploymentID,
              ...bounds,
              limit: 500,
            },
            controller.signal
          )
        );
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load resource logs"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
          if (live) {
            timer = setTimeout(refresh, refreshIntervalMilliseconds);
          }
        }
      }
    };
    void load();
    return () => {
      controller.abort();
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [
    appliedQuery,
    activeDeploymentID,
    kind,
    live,
    projectID,
    refresh,
    refreshVersion,
    resourceID,
    timeFrom,
    timeRange,
    timeTo,
  ]);

  const records = useMemo(() => window?.records ?? [], [window]);
  const severityCounts = useMemo(() => {
    const counts: Record<Exclude<Severity, "all">, number> = {
      debug: 0,
      error: 0,
      info: 0,
      unset: 0,
      warn: 0,
    };
    for (const record of records) {
      counts[severity(record)] += 1;
    }
    return counts;
  }, [records]);
  const filtered = useMemo(() => {
    const bounds = telemetryTimeBounds({
      from: timeFrom,
      range: timeRange,
      to: timeTo,
    });
    return records.filter(
      (record) =>
        (severityFilter === "all" || severity(record) === severityFilter) &&
        (!correlatedOnly || Boolean(record.traceId)) &&
        inTimeRange(record.timestamp, bounds)
    );
  }, [correlatedOnly, records, severityFilter, timeFrom, timeRange, timeTo]);

  const table = useReactTable({
    columns: logColumns,
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
      logLevel: "all",
      timeFrom: null,
      timeRange: "all",
      timeTo: null,
      withTrace: false,
    });
  };
  const activeFilters =
    Number(severityFilter !== "all") +
    Number(timeRange !== "all") +
    Number(correlatedOnly) +
    Number(Boolean(urlDeploymentID && !deploymentID));

  return (
    <div className="min-h-[34rem] border-y border-border bg-background">
      <header
        className={cn(
          "grid min-h-12 border-b border-border max-lg:flex max-lg:flex-wrap max-lg:items-center max-lg:gap-2 max-lg:px-3 max-lg:py-2",
          toolbarColumns(controlsOpen)
        )}
      >
        <div
          className={cn(
            "flex h-12 items-center px-3 max-lg:h-auto max-lg:px-0",
            toolbarFilterCell(controlsOpen)
          )}
        >
          <Button
            aria-pressed={controlsOpen}
            onClick={() => setControlsOpen((value) => !value)}
            size="sm"
            variant="ghost"
          >
            <Filter />
            {controlsOpen ? "Hide filters" : "Show filters"}
          </Button>
        </div>
        <form
          className="relative mx-3 my-2 min-w-64 max-lg:m-0 max-lg:flex-1"
          onSubmit={(event) => {
            event.preventDefault();
            const nextQuery = String(
              new FormData(event.currentTarget).get("query") ?? ""
            ).trim();
            if (nextQuery === appliedQuery) {
              refresh();
            } else {
              setLoading(true);
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
            placeholder="Search message text"
          />
        </form>
        <div className="flex h-12 items-center gap-2 pr-3 max-lg:h-auto max-lg:pr-0">
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
          <span className="px-1 text-[9px] text-muted-foreground tabular-nums">
            {filtered.length.toLocaleString()} /{" "}
            {records.length.toLocaleString()}
          </span>
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
      {window?.truncated ? (
        <div className="flex items-center gap-2 border-b border-amber-500/30 bg-amber-500/5 px-4 py-2 text-[9px] text-amber-700 dark:text-amber-300">
          <AlertTriangle className="size-3.5" /> Showing the latest 500 records.
          Search is executed against the complete retained window.
        </div>
      ) : null}

      <div
        className={cn(
          "grid min-h-[31rem]",
          controlsOpen && "grid-cols-[13rem_minmax(0,1fr)] max-md:block"
        )}
      >
        {controlsOpen ? (
          <aside className="border-r border-border max-md:border-r-0 max-md:border-b">
            <div className="flex h-10 items-center justify-between border-b border-border px-3">
              <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Filters {activeFilters ? `· ${activeFilters}` : ""}
              </span>
              {activeFilters ? (
                <Button
                  aria-label="Reset log filters"
                  onClick={resetFilters}
                  size="icon"
                  variant="ghost"
                >
                  <RotateCcw />
                </Button>
              ) : null}
            </div>
            {activeDeploymentID ? (
              <section className="border-b border-border p-2">
                <p className="px-2 pb-1 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
                  Deployment
                </p>
                <FilterChoice
                  active
                  label={shortID(activeDeploymentID)}
                  onClick={() => {
                    if (!deploymentID) {
                      void setLogState({ deployment: null });
                    }
                  }}
                />
              </section>
            ) : null}
            <section className="border-b border-border p-2">
              <p className="px-2 pb-1 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
                Level
              </p>
              <FilterChoice
                active={severityFilter === "all"}
                count={records.length}
                label="All levels"
                onClick={() => void setLogState({ logLevel: "all" })}
              />
              {(["error", "warn", "info", "debug", "unset"] as const).map(
                (value) => (
                  <FilterChoice
                    active={severityFilter === value}
                    count={severityCounts[value]}
                    key={value}
                    label={value === "unset" ? "No level" : value}
                    onClick={() => void setLogState({ logLevel: value })}
                  />
                )
              )}
            </section>
            <section className="p-2">
              <FilterChoice
                active={correlatedOnly}
                count={records.filter((record) => record.traceId).length}
                label="Has trace context"
                onClick={() => void setLogState({ withTrace: !correlatedOnly })}
              />
            </section>
          </aside>
        ) : null}

        <div className="min-w-0 overflow-auto">
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
