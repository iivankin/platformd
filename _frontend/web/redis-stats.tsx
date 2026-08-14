import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { fetchManagedRedisStats, fetchManagedRedisStatsHistory } from "@/api";
import type { ManagedRedisStats } from "@/api";
import { Button } from "@/components/ui/button";
import {
  ManagedMetricChart,
  ManagedStatsRangePicker,
  foldUnselectedOperationsIntoOther,
  formatCompact,
  formatMicros,
  formatStatsBytes,
  formatStatsRate,
  managedHistoryEmptyLabel,
  managedStatsChartColors,
  operationsBreakdownKeys,
  useManagedStatsHistory,
} from "@/managed-stats-charts";
import { formatTTL } from "@/redis-data-utils";

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

export const RedisStats = ({
  projectID,
  range: controlledRange,
  redisID,
  showRange = true,
}: {
  projectID: string;
  range?: Parameters<typeof fetchManagedRedisStatsHistory>[2];
  redisID: string;
  showRange?: boolean;
}) => {
  const [stats, setStats] = useState<ManagedRedisStats>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setStats(await fetchManagedRedisStats(projectID, redisID));
      setError(undefined);
    } catch (loadError) {
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to load Redis stats"
      );
    } finally {
      setLoading(false);
    }
  }, [projectID, redisID]);

  useEffect(() => {
    const controller = new AbortController();
    const loadInitialStats = async () => {
      try {
        const loaded = await fetchManagedRedisStats(
          projectID,
          redisID,
          controller.signal
        );
        setStats(loaded);
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
            : "Unable to load Redis stats"
        );
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void loadInitialStats();
    return () => controller.abort();
  }, [projectID, redisID]);

  const fetchHistory = useCallback(
    (
      range: Parameters<typeof fetchManagedRedisStatsHistory>[2],
      signal: AbortSignal
    ) => fetchManagedRedisStatsHistory(projectID, redisID, range, signal),
    [projectID, redisID]
  );
  const { history, historyError, range, setRange } = useManagedStatsHistory(
    fetchHistory,
    controlledRange
  );
  const emptyLabel = managedHistoryEmptyLabel(history, historyError);
  const operationsKeys = useMemo(
    () =>
      operationsBreakdownKeys({
        history,
        otherKey: "otherCommandsPerSecond",
        prefix: "cmd.",
      }),
    [history]
  );
  const operationsHistory = useMemo(
    () =>
      foldUnselectedOperationsIntoOther(history, operationsKeys, {
        otherKey: "otherCommandsPerSecond",
        prefix: "cmd.",
      }),
    [history, operationsKeys]
  );

  const hitRate = useMemo(() => {
    if (!stats) {
      return 0;
    }
    const total = stats.keyspaceHits + stats.keyspaceMisses;
    return total === 0 ? 0 : (stats.keyspaceHits / total) * 100;
  }, [stats]);

  return (
    <section className="border border-border bg-card">
      <header className="flex items-center justify-between border-b border-border px-5 py-3">
        <h3 className="text-[10px] font-medium">Live Redis statistics</h3>
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
          <div className="grid grid-cols-3 border-b border-border lg:grid-cols-6">
            <Stat label="Version" value={stats.version} />
            <Stat
              label="Uptime"
              value={formatTTL(stats.uptimeSeconds * 1000)}
            />
            <Stat
              label="Clients"
              value={formatCompact(stats.connectedClients)}
            />
            <Stat
              label="Blocked"
              value={formatCompact(stats.blockedClients)}
              warning={stats.blockedClients > 0}
            />
            <Stat
              label="Rejected"
              value={formatCompact(stats.rejectedConnections)}
              warning={stats.rejectedConnections > 0}
            />
            <Stat
              label="Ops/sec"
              value={formatCompact(stats.operationsPerSecond)}
            />
          </div>
          <div className="grid grid-cols-3 border-b border-border lg:grid-cols-6">
            <Stat
              label="Used memory"
              value={formatStatsBytes(stats.usedMemoryBytes)}
            />
            <Stat
              label="RSS memory"
              value={formatStatsBytes(stats.rssMemoryBytes)}
            />
            <Stat
              label="Peak memory"
              value={formatStatsBytes(stats.peakMemoryBytes)}
            />
            <Stat
              label="Fragmentation"
              value={stats.fragmentationRatio.toFixed(2)}
              warning={stats.fragmentationRatio > 1.5}
            />
            <Stat
              label="Max memory"
              value={
                stats.maxMemoryBytes === 0
                  ? "Unlimited"
                  : formatStatsBytes(stats.maxMemoryBytes)
              }
            />
            <Stat
              label="Hit rate"
              value={`${hitRate.toFixed(1)}%`}
              warning={hitRate < 80}
            />
          </div>
          <div className="grid grid-cols-3 border-b border-border lg:grid-cols-6">
            <Stat label="Eviction" value={stats.evictionPolicy} />
            <Stat
              label="Expired keys"
              value={formatCompact(stats.expiredKeys)}
            />
            <Stat
              label="Evicted keys"
              value={formatCompact(stats.evictedKeys)}
              warning={stats.evictedKeys > 0}
            />
            <Stat
              label="AOF"
              value={stats.aofEnabled ? "Enabled" : "Disabled"}
            />
            <Stat
              label="Total commands"
              value={formatCompact(stats.totalCommands)}
            />
            <Stat
              label="Total connections"
              value={formatCompact(stats.totalConnections)}
            />
          </div>
          <div className="grid grid-cols-2 border-b border-border lg:grid-cols-4">
            <Stat
              label="Net input"
              value={formatStatsBytes(stats.totalNetInputBytes)}
            />
            <Stat
              label="Net output"
              value={formatStatsBytes(stats.totalNetOutputBytes)}
            />
            <Stat
              label="Latency p50"
              value={formatMicros(stats.latencyP50Micros)}
            />
            <Stat
              label="Latency p99"
              value={formatMicros(stats.latencyP99Micros)}
            />
          </div>
        </>
      ) : (
        <div className="grid min-h-24 place-items-center border-b border-border px-6 text-[10px] text-muted-foreground">
          {error ?? (loading ? "Loading Redis stats…" : "Stats unavailable")}
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
            history={operationsHistory}
            keys={operationsKeys}
            minimumMaximum={1}
            title="Operations"
          />
        </div>
        <div className="min-w-0 border-t border-border lg:border-t-0">
          <ManagedMetricChart
            emptyLabel={emptyLabel}
            formatValue={formatMicros}
            history={history}
            keys={[
              {
                color: managedStatsChartColors.primary,
                key: "latencyP50Micros",
                label: "p50",
              },
              {
                color: managedStatsChartColors.secondary,
                key: "latencyP95Micros",
                label: "p95",
              },
              {
                color: managedStatsChartColors.danger,
                key: "latencyP99Micros",
                label: "p99",
              },
            ]}
            minimumMaximum={1}
            title="Latency"
          />
        </div>
        <div className="min-w-0 border-t border-border lg:border-r lg:border-border">
          <ManagedMetricChart
            emptyLabel={emptyLabel}
            formatValue={formatStatsBytes}
            history={history}
            keys={[
              {
                color: managedStatsChartColors.secondary,
                key: "usedMemoryBytes",
                label: "Used",
              },
            ]}
            minimumMaximum={1024 ** 2}
            title="Memory"
          />
        </div>
        <div className="min-w-0 border-t border-border">
          <ManagedMetricChart
            emptyLabel={emptyLabel}
            formatValue={(value) => `${value.toFixed(1)}%`}
            history={history}
            keys={[
              {
                color: managedStatsChartColors.tertiary,
                key: "hitRatePercent",
                label: "Hit rate",
              },
            ]}
            minimumMaximum={100}
            title="Hit rate"
          />
        </div>
        <div className="min-w-0 border-t border-border lg:col-span-2">
          <ManagedMetricChart
            emptyLabel={emptyLabel}
            formatValue={formatStatsRate}
            history={history}
            keys={[
              {
                color: managedStatsChartColors.primary,
                key: "netInputBytesPerSecond",
                label: "Input",
              },
              {
                color: managedStatsChartColors.secondary,
                key: "netOutputBytesPerSecond",
                label: "Output",
              },
            ]}
            minimumMaximum={1024}
            title="Network"
          />
        </div>
      </div>

      {stats ? (
        <>
          <header className="border-b border-border px-5 py-3 text-[10px] font-medium">
            Command stats
          </header>
          <table className="w-full border-collapse text-left text-[10px]">
            <thead>
              <tr className="border-b border-border">
                <th className="px-5 py-2 font-medium">Command</th>
                <th className="px-5 py-2 text-right font-medium">Calls</th>
                <th className="px-5 py-2 text-right font-medium">Avg</th>
                <th className="px-5 py-2 text-right font-medium">p50</th>
                <th className="px-5 py-2 text-right font-medium">p95</th>
                <th className="px-5 py-2 text-right font-medium">p99</th>
                <th className="px-5 py-2 text-right font-medium">Total time</th>
              </tr>
            </thead>
            <tbody>
              {stats.commands.slice(0, 30).map((command) => (
                <tr className="border-b border-border" key={command.name}>
                  <td className="px-5 py-2 font-mono uppercase">
                    {command.name}
                  </td>
                  <td className="px-5 py-2 text-right">
                    {formatCompact(command.calls)}
                  </td>
                  <td className="px-5 py-2 text-right">
                    {formatMicros(command.microsPerCall)}
                  </td>
                  <td className="px-5 py-2 text-right">
                    {formatMicros(command.p50Micros)}
                  </td>
                  <td className="px-5 py-2 text-right">
                    {formatMicros(command.p95Micros)}
                  </td>
                  <td className="px-5 py-2 text-right">
                    {formatMicros(command.p99Micros)}
                  </td>
                  <td className="px-5 py-2 text-right">
                    {(command.totalMicros / 1_000_000).toFixed(2)}s
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <header className="border-b border-border px-5 py-3 text-[10px] font-medium">
            Slowlog
          </header>
          {stats.slowlog.length === 0 ? (
            <p className="border-b border-border px-5 py-4 text-[10px] text-muted-foreground">
              No slowlog entries
            </p>
          ) : (
            <table className="w-full border-collapse text-left text-[10px]">
              <thead>
                <tr className="border-b border-border">
                  <th className="px-5 py-2 font-medium">ID</th>
                  <th className="px-5 py-2 text-right font-medium">When</th>
                  <th className="px-5 py-2 text-right font-medium">Duration</th>
                  <th className="px-5 py-2 text-right font-medium">Client</th>
                  <th className="px-5 py-2 font-medium">Command</th>
                </tr>
              </thead>
              <tbody>
                {stats.slowlog.map((entry) => (
                  <tr className="border-b border-border" key={entry.id}>
                    <td className="px-5 py-2 font-mono">{entry.id}</td>
                    <td className="px-5 py-2 text-right">
                      {new Date(entry.timestampMillis).toLocaleString()}
                    </td>
                    <td className="px-5 py-2 text-right">
                      {formatMicros(entry.durationMicros)}
                    </td>
                    <td className="px-5 py-2 text-right font-mono">
                      {entry.client || "—"}
                    </td>
                    <td className="max-w-lg truncate px-5 py-2 font-mono">
                      {entry.command}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          <header className="border-b border-border px-5 py-3 text-[10px] font-medium">
            Keyspaces
          </header>
          {stats.keyspaces.map((keyspace) => (
            <div
              className="grid grid-cols-4 border-b border-border px-5 py-2 text-[10px]"
              key={keyspace.database}
            >
              <code>{keyspace.database}</code>
              <span>{formatCompact(keyspace.keys)} keys</span>
              <span>{formatCompact(keyspace.expires)} expire</span>
              <span className="text-right">
                avg TTL {formatTTL(keyspace.averageTtlMillis)}
              </span>
            </div>
          ))}
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
