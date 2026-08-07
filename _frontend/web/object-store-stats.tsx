import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { fetchObjectStoreStats, fetchObjectStoreStatsHistory } from "@/api";
import type { ObjectStoreStats as Stats } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import {
  ManagedMetricChart,
  ManagedStatsRangePicker,
  formatCompact,
  formatMicros,
  formatStatsBytes,
  formatStatsRate,
  managedHistoryEmptyLabel,
  managedStatsChartColors,
  objectStoreOperationsBreakdown,
  operationsBreakdownKeys,
  useManagedStatsHistory,
} from "@/managed-stats-charts";
import { ObjectStoreLargestObjects } from "@/object-store-largest-objects";

const formatSnapshotAge = (observedAt?: number) => {
  if (!observedAt) {
    return "Pending";
  }
  const ageSeconds = Math.max(0, Math.floor((Date.now() - observedAt) / 1000));
  if (ageSeconds < 60) {
    return "Just now";
  }
  if (ageSeconds < 3600) {
    return `${Math.floor(ageSeconds / 60)}m ago`;
  }
  if (ageSeconds < 86_400) {
    return `${Math.floor(ageSeconds / 3600)}h ago`;
  }
  return `${Math.floor(ageSeconds / 86_400)}d ago`;
};

const Stat = ({ label, value }: { label: string; value: string }) => (
  <div className="border-r border-border px-4 py-3 last:border-r-0">
    <p className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
      {label}
    </p>
    <p className="mt-1 text-xs">{value}</p>
  </div>
);

const inventoryStatus = ({
  error,
  loading,
}: {
  error?: string;
  loading: boolean;
}): { detail?: string; title: string } => {
  if (loading) {
    return { title: "Loading storage stats…" };
  }
  if (error) {
    return { title: error };
  }
  return {
    detail:
      "The first usage snapshot is being built in the background. This page will not wait for the full object scan.",
    title: "Calculating inventory…",
  };
};

const TrafficStats = ({
  meanLatency,
  traffic,
  trafficOps,
}: {
  meanLatency: number;
  traffic: NonNullable<Stats["traffic"]>;
  trafficOps: [string, number][];
}) => (
  <>
    <header className="border-b border-border px-5 py-3">
      <h4 className="text-[10px] font-medium">Traffic</h4>
      <p className="mt-1 text-[9px] text-muted-foreground">
        Cumulative request counters since sidecar start.
      </p>
    </header>
    <div className="grid grid-cols-2 border-b border-border lg:grid-cols-5">
      <Stat label="Bytes in" value={formatStatsBytes(traffic.bytesIn)} />
      <Stat label="Bytes out" value={formatStatsBytes(traffic.bytesOut)} />
      <Stat label="Errors" value={formatCompact(traffic.errors)} />
      <Stat label="Active" value={formatCompact(traffic.activeRequests)} />
      <Stat label="Mean latency" value={formatMicros(meanLatency)} />
    </div>
    {trafficOps.length > 0 ? (
      <div className="divide-y divide-border border-b border-border">
        {trafficOps.map(([name, count]) => (
          <div
            className="grid grid-cols-[8rem_minmax(0,1fr)_4rem] items-center gap-4 px-5 py-2 text-[10px]"
            key={name}
          >
            <code className="text-muted-foreground">{name}</code>
            <div className="h-1.5 bg-muted">
              <div
                className="h-full bg-foreground"
                style={{
                  width: `${(count / Math.max(1, trafficOps[0]?.[1] ?? 1)) * 100}%`,
                }}
              />
            </div>
            <span className="text-right">{formatCompact(count)}</span>
          </div>
        ))}
      </div>
    ) : null}
  </>
);

export const ObjectStoreStats = ({
  projectID,
  storeID,
}: {
  projectID: string;
  storeID: string;
}) => {
  const [stats, setStats] = useState<Stats>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setStats(await fetchObjectStoreStats(projectID, storeID));
      setError(undefined);
    } catch (loadError) {
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to load storage stats"
      );
    } finally {
      setLoading(false);
    }
  }, [projectID, storeID]);

  useEffect(() => {
    const controller = new AbortController();
    const loadInitialStats = async () => {
      try {
        setStats(
          await fetchObjectStoreStats(projectID, storeID, controller.signal)
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
            : "Unable to load storage stats"
        );
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void loadInitialStats();
    return () => controller.abort();
  }, [projectID, storeID]);

  const fetchHistory = useCallback(
    (
      range: Parameters<typeof fetchObjectStoreStatsHistory>[2],
      signal: AbortSignal
    ) => fetchObjectStoreStatsHistory(projectID, storeID, range, signal),
    [projectID, storeID]
  );
  const { history, historyError, range, setRange } =
    useManagedStatsHistory(fetchHistory);
  const emptyLabel = managedHistoryEmptyLabel(history, historyError);
  const operationsKeys = useMemo(
    () =>
      operationsBreakdownKeys({
        fixed: objectStoreOperationsBreakdown,
        history,
      }),
    [history]
  );

  const averageBytes = stats?.objectCount
    ? stats.totalBytes / stats.objectCount
    : 0;
  const largestBucket = Math.max(
    1,
    ...(stats?.objectSizeHistogram.map((bucket) => bucket.count) ?? [])
  );
  const traffic = stats?.traffic;
  const trafficOps = traffic
    ? Object.entries(traffic.ops).toSorted((left, right) => right[1] - left[1])
    : [];
  const meanLatency =
    traffic &&
    Object.values(traffic.ops).reduce((sum, count) => sum + count, 0) > 0
      ? traffic.totalLatencyMicros /
        Object.values(traffic.ops).reduce((sum, count) => sum + count, 0)
      : 0;
  const inventory = inventoryStatus({ error, loading });

  return (
    <div className="space-y-4">
      <SectionCard>
        <header className="flex items-center justify-between border-b border-border px-5 py-3">
          <div>
            <h3 className="text-[10px] font-medium">Storage statistics</h3>
            <p className="mt-1 text-[9px] text-muted-foreground">
              Persisted usage snapshot refreshed in the background.
            </p>
          </div>
          <Button
            aria-label="Refresh storage statistics"
            disabled={loading}
            onClick={() => void load()}
            size="icon"
            variant="ghost"
          >
            <RefreshCw className={loading ? "animate-spin" : undefined} />
          </Button>
        </header>

        {traffic ? (
          <TrafficStats
            meanLatency={meanLatency}
            traffic={traffic}
            trafficOps={trafficOps}
          />
        ) : null}

        {stats?.ready ? (
          <>
            <div className="grid grid-cols-2 border-b border-border lg:grid-cols-4">
              <Stat label="Objects" value={formatCompact(stats.objectCount)} />
              <Stat
                label="Stored data"
                value={formatStatsBytes(stats.totalBytes)}
              />
              <Stat
                label="Average object"
                value={formatStatsBytes(averageBytes)}
              />
              <Stat
                label="Snapshot"
                value={formatSnapshotAge(stats.observedAt)}
              />
            </div>
            <header className="border-b border-border px-5 py-3">
              <h4 className="text-[10px] font-medium">
                Object size distribution
              </h4>
              <p className="mt-1 text-[9px] text-muted-foreground">
                Distribution from the last completed background scan.
              </p>
            </header>
            <div className="divide-y divide-border border-b border-border">
              {stats.objectSizeHistogram.map((bucket) => (
                <div
                  className="grid grid-cols-[6.5rem_minmax(0,1fr)_4rem] items-center gap-4 px-5 py-2.5 text-[10px]"
                  key={bucket.label}
                >
                  <span className="text-muted-foreground">{bucket.label}</span>
                  <div className="h-1.5 bg-muted">
                    <div
                      className="h-full bg-foreground"
                      style={{
                        width: `${(bucket.count / largestBucket) * 100}%`,
                      }}
                    />
                  </div>
                  <span className="text-right">
                    {formatCompact(bucket.count)}
                  </span>
                </div>
              ))}
            </div>
          </>
        ) : (
          <div className="grid min-h-52 place-items-center border-b border-border px-6 text-center text-[10px] text-muted-foreground">
            <div>
              <p className="text-foreground">{inventory.title}</p>
              {inventory.detail ? (
                <p className="mt-2 max-w-md leading-5">{inventory.detail}</p>
              ) : null}
            </div>
          </div>
        )}

        <ManagedStatsRangePicker
          history={history}
          historyError={historyError}
          onChange={setRange}
          range={range}
        />
        <div className="grid border-b border-border lg:grid-cols-2">
          <div className="min-w-0 lg:border-r lg:border-border">
            <ManagedMetricChart
              emptyLabel={emptyLabel}
              formatValue={(value) => formatCompact(value)}
              history={history}
              keys={[
                {
                  color: managedStatsChartColors.primary,
                  key: "objectCount",
                  label: "Objects",
                },
              ]}
              minimumMaximum={1}
              title="Object count"
            />
          </div>
          <div className="min-w-0 border-t border-border lg:border-t-0">
            <ManagedMetricChart
              emptyLabel={emptyLabel}
              formatValue={formatStatsBytes}
              history={history}
              keys={[
                {
                  color: managedStatsChartColors.secondary,
                  key: "totalBytes",
                  label: "Stored",
                },
              ]}
              minimumMaximum={1024 ** 2}
              title="Stored bytes"
            />
          </div>
          <div className="min-w-0 border-t border-border lg:border-r lg:border-border">
            <ManagedMetricChart
              emptyLabel={emptyLabel}
              formatValue={(value) => formatCompact(value)}
              history={history}
              keys={operationsKeys}
              minimumMaximum={1}
              title="Operations"
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
                  key: "bytesInPerSecond",
                  label: "In",
                },
                {
                  color: managedStatsChartColors.secondary,
                  key: "bytesOutPerSecond",
                  label: "Out",
                },
              ]}
              minimumMaximum={1024}
              title="Bytes / second"
            />
          </div>
          <div className="min-w-0 border-t border-border lg:col-span-2">
            <ManagedMetricChart
              emptyLabel={emptyLabel}
              formatValue={formatMicros}
              history={history}
              keys={[
                {
                  color: managedStatsChartColors.danger,
                  key: "meanLatencyMicros",
                  label: "Mean",
                },
              ]}
              minimumMaximum={1}
              title="Latency"
            />
          </div>
        </div>

        {error ? (
          <p
            aria-live="polite"
            className="border-b border-border px-5 py-3 text-[10px] text-destructive"
          >
            {error}
          </p>
        ) : null}
      </SectionCard>
      <ObjectStoreLargestObjects
        estimatedObjects={stats?.ready ? stats.objectCount : undefined}
        projectID={projectID}
        storeID={storeID}
      />
    </div>
  );
};
