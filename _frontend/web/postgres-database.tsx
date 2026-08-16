import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";

import {
  fetchManagedPostgresStats,
  fetchManagedPostgresStatsHistory,
} from "@/api";
import type { ManagedPostgresStats } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  ManagedMetricChart,
  ManagedStatsRangePicker,
  formatCompact,
  formatMillis,
  formatStatsBytes,
  formatStatsRate,
  managedHistoryEmptyLabel,
  managedStatsChartColors,
  useManagedStatsHistory,
} from "@/managed-stats-charts";
import { PostgresDataBrowser } from "@/postgres-data-browser";
import { PostgresExtensions } from "@/postgres-extensions";
import { PostgresQueryRunner } from "@/postgres-query-runner";

type DatabaseView = "data" | "extensions" | "query";

const Stat = ({
  label,
  value,
  warning = false,
}: {
  label: string;
  value: string;
  warning?: boolean;
}) => (
  <div className="border-r border-border px-4 py-3 last:border-r-0">
    <p className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
      {label}
    </p>
    <p className={warning ? "mt-1 text-xs text-amber-600" : "mt-1 text-xs"}>
      {value}
    </p>
  </div>
);

const RateChip = ({ label, value }: { label: string; value: string }) => (
  <span className="border border-border px-2 py-1 text-[9px] text-muted-foreground">
    <span className="text-foreground">{value}</span> {label}
  </span>
);

const truncate = (value: string, limit = 120) =>
  value.length <= limit ? value : `${value.slice(0, limit)}…`;

const StatsTable = ({
  children,
  empty,
  headers,
  title,
}: {
  children: ReactNode;
  empty: boolean;
  headers: string[];
  title: string;
}) => (
  <section className="border-b border-border">
    <header className="border-b border-border px-5 py-3 text-[10px] font-medium">
      {title}
    </header>
    {empty ? (
      <p className="px-5 py-4 text-[10px] text-muted-foreground">None</p>
    ) : (
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-left text-[10px]">
          <thead>
            <tr className="border-b border-border">
              {headers.map((header, index) => (
                <th
                  className={cn(
                    "px-5 py-2 font-medium whitespace-nowrap",
                    index > 0 && "text-right"
                  )}
                  key={header}
                >
                  {header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>{children}</tbody>
        </table>
      </div>
    )}
  </section>
);

export const PostgresStats = ({
  onOpenInQuery,
  postgresID,
  projectID,
  range: controlledRange,
  showRange = true,
}: {
  onOpenInQuery?: (sql: string) => void;
  postgresID: string;
  projectID: string;
  range?: Parameters<typeof fetchManagedPostgresStatsHistory>[2];
  showRange?: boolean;
}) => {
  const [stats, setStats] = useState<ManagedPostgresStats>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setStats(await fetchManagedPostgresStats(projectID, postgresID));
      setError(undefined);
    } catch (loadError) {
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to load PostgreSQL stats"
      );
    } finally {
      setLoading(false);
    }
  }, [postgresID, projectID]);

  useEffect(() => {
    const controller = new AbortController();
    const loadInitial = async () => {
      try {
        setStats(
          await fetchManagedPostgresStats(
            projectID,
            postgresID,
            controller.signal
          )
        );
        setError(undefined);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load PostgreSQL stats"
        );
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void loadInitial();
    return () => controller.abort();
  }, [postgresID, projectID]);

  const fetchHistory = useCallback(
    (
      range: Parameters<typeof fetchManagedPostgresStatsHistory>[2],
      signal: AbortSignal
    ) => fetchManagedPostgresStatsHistory(projectID, postgresID, range, signal),
    [postgresID, projectID]
  );
  const { history, historyError, range, setRange } = useManagedStatsHistory(
    fetchHistory,
    controlledRange
  );
  const emptyLabel = managedHistoryEmptyLabel(history, historyError);

  const rateChips = stats
    ? [
        {
          label: "QPS",
          show: stats.queriesPerSecond > 0,
          value: formatCompact(stats.queriesPerSecond),
        },
        {
          label: "TPS",
          show: stats.transactionsPerSecond > 0,
          value: formatCompact(stats.transactionsPerSecond),
        },
        {
          label: "rows read/s",
          show: stats.rowsReadPerSecond > 0,
          value: formatCompact(stats.rowsReadPerSecond),
        },
        {
          label: "rows written/s",
          show: stats.rowsWrittenPerSecond > 0,
          value: formatCompact(stats.rowsWrittenPerSecond),
        },
        {
          label: "bytes read/s",
          show: stats.bytesReadPerSecond > 0,
          value: formatStatsRate(stats.bytesReadPerSecond),
        },
        {
          label: "mean latency",
          show: stats.meanQueryLatencyMillis > 0,
          value: formatMillis(stats.meanQueryLatencyMillis),
        },
      ].filter((chip) => chip.show)
    : [];

  const unusedIndexes =
    stats?.indexes.filter((index) => index.idxScan === 0) ?? [];

  return (
    <section className="border border-border bg-card">
      <header className="flex items-center justify-between border-b border-border px-5 py-3">
        <h3 className="text-[10px] font-medium">Live PostgreSQL statistics</h3>
        <Button
          disabled={loading}
          onClick={() => void load()}
          size="icon"
          variant="ghost"
        >
          <RefreshCw className={loading ? "animate-spin" : undefined} />
        </Button>
      </header>
      {stats ? (
        <>
          <div className="grid grid-cols-3 border-b border-border lg:grid-cols-7">
            <Stat label="Version" value={stats.version} />
            <Stat
              label="Size"
              value={formatStatsBytes(stats.databaseSizeBytes)}
            />
            <Stat
              label="Connections"
              value={formatCompact(stats.connections)}
            />
            <Stat label="Active" value={formatCompact(stats.active)} />
            <Stat label="Idle" value={formatCompact(stats.idle)} />
            <Stat
              label="Idle in tx"
              value={formatCompact(stats.idleInTransaction)}
              warning={stats.idleInTransaction > 0}
            />
            <Stat
              label="Cache hit"
              value={`${stats.cacheHitPercent.toFixed(1)}%`}
              warning={stats.cacheHitPercent < 90}
            />
          </div>
          {rateChips.length > 0 ? (
            <div className="flex flex-wrap gap-2 border-b border-border px-5 py-3">
              {rateChips.map((chip) => (
                <RateChip
                  key={chip.label}
                  label={chip.label}
                  value={chip.value}
                />
              ))}
            </div>
          ) : null}
        </>
      ) : (
        <div className="grid min-h-24 place-items-center border-b border-border px-6 text-[10px] text-muted-foreground">
          {error ??
            (loading ? "Loading PostgreSQL stats…" : "Stats unavailable")}
        </div>
      )}

      {showRange ? (
        <ManagedStatsRangePicker
          history={history}
          historyError={historyError}
          onChange={setRange}
          range={range}
        />
      ) : null}
      <div className="grid border-b border-border lg:grid-cols-2">
        <div className="min-w-0 lg:border-r lg:border-border">
          <ManagedMetricChart
            emptyLabel={emptyLabel}
            formatValue={(value) => formatCompact(value)}
            history={history}
            keys={[
              {
                color: managedStatsChartColors.primary,
                key: "queriesPerSecond",
                label: "QPS",
              },
              {
                color: managedStatsChartColors.secondary,
                key: "transactionsPerSecond",
                label: "TPS",
              },
            ]}
            minimumMaximum={1}
            title="Throughput"
          />
        </div>
        <div className="min-w-0 border-t border-border lg:border-t-0">
          <ManagedMetricChart
            emptyLabel={emptyLabel}
            formatValue={formatMillis}
            history={history}
            keys={[
              {
                color: managedStatsChartColors.primary,
                key: "meanQueryLatencyMillis",
                label: "Mean",
              },
            ]}
            minimumMaximum={1}
            title="Query latency"
          />
        </div>
        <div className="min-w-0 border-t border-border lg:border-r lg:border-border">
          <ManagedMetricChart
            emptyLabel={emptyLabel}
            formatValue={(value) => formatCompact(value)}
            history={history}
            keys={[
              {
                color: managedStatsChartColors.primary,
                key: "rowsReadPerSecond",
                label: "Read",
              },
              {
                color: managedStatsChartColors.secondary,
                key: "rowsWrittenPerSecond",
                label: "Written",
              },
            ]}
            minimumMaximum={1}
            title="Rows / second"
          />
        </div>
        <div className="min-w-0 border-t border-border">
          <ManagedMetricChart
            emptyLabel={emptyLabel}
            formatValue={formatStatsRate}
            history={history}
            keys={[
              {
                color: managedStatsChartColors.primary,
                key: "bytesReadPerSecond",
                label: "Read",
              },
              {
                color: managedStatsChartColors.tertiary,
                key: "bytesHitPerSecond",
                label: "Cache hit",
              },
            ]}
            minimumMaximum={1024}
            title="Bytes / second"
          />
        </div>
      </div>

      {stats ? (
        <>
          <StatsTable
            empty={stats.sessions.length === 0}
            headers={[
              "PID",
              "User",
              "State",
              "Wait",
              "Duration",
              "Client",
              "Query",
            ]}
            title="Sessions"
          >
            {stats.sessions.map((session) => (
              <tr className="border-b border-border" key={session.pid}>
                <td className="px-5 py-2 font-mono">{session.pid}</td>
                <td className="px-5 py-2 text-right">
                  {session.usename || "—"}
                </td>
                <td className="px-5 py-2 text-right">{session.state || "—"}</td>
                <td className="px-5 py-2 text-right">
                  {session.waitEvent || "—"}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatMillis(session.durationMillis)}
                </td>
                <td className="px-5 py-2 text-right">
                  {session.clientAddr || "—"}
                </td>
                <td className="max-w-md truncate px-5 py-2 font-mono">
                  {truncate(session.query) || "—"}
                </td>
              </tr>
            ))}
          </StatsTable>

          <StatsTable
            empty={stats.statements.length === 0}
            headers={[
              "Query",
              "Calls",
              "Total",
              "Mean",
              "Max",
              "% time",
              "Rows",
              "",
            ]}
            title="Statements"
          >
            {stats.statements.map((statement) => (
              <tr className="border-b border-border" key={statement.queryId}>
                <td className="max-w-lg truncate px-5 py-2 font-mono">
                  {truncate(statement.query)}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(statement.calls)}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatMillis(statement.totalExecTimeMillis)}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatMillis(statement.meanExecTimeMillis)}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatMillis(statement.maxExecTimeMillis)}
                </td>
                <td className="px-5 py-2 text-right">
                  {statement.percentOfTotalTime.toFixed(1)}%
                </td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(statement.rows)}
                </td>
                <td className="px-5 py-2 text-right">
                  {onOpenInQuery ? (
                    <button
                      className="text-[9px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                      onClick={() => onOpenInQuery(statement.query)}
                      type="button"
                    >
                      Open in Query
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
          </StatsTable>

          <StatsTable
            empty={stats.blocked.length === 0}
            headers={[
              "Blocked",
              "User",
              "For",
              "Blocking",
              "Wait",
              "Blocked query",
            ]}
            title="Blocked"
          >
            {stats.blocked.map((item) => (
              <tr
                className="border-b border-border"
                key={`${item.blockedPid}-${item.blockingPid}`}
              >
                <td className="px-5 py-2 font-mono">{item.blockedPid}</td>
                <td className="px-5 py-2 text-right">
                  {item.blockedUser || "—"}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatMillis(item.blockedForMillis)}
                </td>
                <td className="px-5 py-2 text-right font-mono">
                  {item.blockingPid}
                </td>
                <td className="px-5 py-2 text-right">
                  {[item.waitEventType, item.waitEvent]
                    .filter(Boolean)
                    .join(" / ") || "—"}
                </td>
                <td className="max-w-md truncate px-5 py-2 font-mono">
                  {truncate(item.blockedQuery) || "—"}
                </td>
              </tr>
            ))}
          </StatsTable>

          <StatsTable
            empty={unusedIndexes.length === 0}
            headers={["Schema", "Table", "Index", "Scans", "Size"]}
            title="Unused indexes"
          >
            {unusedIndexes.map((index) => (
              <tr
                className="border-b border-border"
                key={`${index.schema}.${index.table}.${index.index}`}
              >
                <td className="px-5 py-2 font-mono">{index.schema}</td>
                <td className="px-5 py-2 text-right font-mono">
                  {index.table}
                </td>
                <td className="px-5 py-2 text-right font-mono">
                  {index.index}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(index.idxScan)}
                </td>
                <td className="px-5 py-2 text-right">{index.sizePretty}</td>
              </tr>
            ))}
          </StatsTable>

          <StatsTable
            empty={stats.sequentialScans.length === 0}
            headers={[
              "Table",
              "Seq scans",
              "Seq rows",
              "Idx scans",
              "Live rows",
              "Size",
            ]}
            title="Sequential scans"
          >
            {stats.sequentialScans.map((scan) => (
              <tr className="border-b border-border" key={scan.table}>
                <td className="px-5 py-2 font-mono">{scan.table}</td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(scan.seqScan)}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(scan.seqTupRead)}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(scan.idxScan)}
                </td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(scan.nLiveTup)}
                </td>
                <td className="px-5 py-2 text-right">{scan.sizePretty}</td>
              </tr>
            ))}
          </StatsTable>

          <StatsTable
            empty={stats.tables.length === 0}
            headers={[
              "Table",
              "Rows",
              "Total",
              "Data",
              "Indexes",
              "Dead rows",
              "Last vacuum",
            ]}
            title="Tables and vacuum"
          >
            {stats.tables.map((table) => (
              <tr className="border-b border-border" key={table.table}>
                <td className="px-5 py-2 font-mono">{table.table}</td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(table.rows)}
                </td>
                <td className="px-5 py-2 text-right">{table.totalPretty}</td>
                <td className="px-5 py-2 text-right">{table.dataPretty}</td>
                <td className="px-5 py-2 text-right">{table.indexesPretty}</td>
                <td className="px-5 py-2 text-right">
                  {formatCompact(table.deadRows)}
                </td>
                <td className="px-5 py-2 text-right">{table.lastVacuum}</td>
              </tr>
            ))}
          </StatsTable>
        </>
      ) : null}

      {error ? (
        <p className="border-b border-border px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </section>
  );
};

export const PostgresDatabase = ({
  postgresID,
  projectID,
}: {
  postgresID: string;
  projectID: string;
}) => {
  const [view, setView] = useState<DatabaseView>("data");
  const [queryDraft, setQueryDraft] = useState<string>();

  const openInQuery = (sql: string) => {
    setQueryDraft(sql);
    setView("query");
  };

  const views: { label: string; value: DatabaseView }[] = [
    { label: "Data", value: "data" },
    { label: "Query", value: "query" },
    { label: "Extensions", value: "extensions" },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <nav
        className="flex min-h-10 shrink-0 border-b border-border px-4"
        aria-label="PostgreSQL database pages"
      >
        {views.map((item) => (
          <button
            className={cn(
              "border-b-2 border-transparent px-4 text-[10px] text-muted-foreground",
              view === item.value && "border-foreground text-foreground"
            )}
            key={item.value}
            onClick={() => setView(item.value)}
            type="button"
          >
            {item.label}
          </button>
        ))}
      </nav>
      <div
        className={cn(
          "min-h-0 flex-1",
          view === "extensions" ? "overflow-auto" : "overflow-hidden"
        )}
      >
        <div className="h-full min-h-0" hidden={view !== "data"}>
          <PostgresDataBrowser
            onOpenInQuery={openInQuery}
            postgresID={postgresID}
            projectID={projectID}
          />
        </div>
        {view === "query" ? (
          <PostgresQueryRunner
            initialSQL={queryDraft}
            postgresID={postgresID}
            projectID={projectID}
          />
        ) : null}
        {view === "extensions" ? (
          <PostgresExtensions postgresID={postgresID} projectID={projectID} />
        ) : null}
      </div>
    </div>
  );
};
