import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { fetchObjectStoreStats } from "@/api";
import type { ObjectStoreStats as Stats } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { ObjectStoreLargestObjects } from "@/object-store-largest-objects";

const compact = (value: number) =>
  Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
    notation: "compact",
  }).format(value);

const formatBytes = (value: number) => {
  if (value === 0) {
    return "0 B";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const unit = Math.min(
    Math.max(0, Math.floor(Math.log(value) / Math.log(1024))),
    units.length - 1
  );
  const precision = unit === 0 && Number.isInteger(value) ? 0 : 1;
  return `${(value / 1024 ** unit).toFixed(precision)} ${units[unit]}`;
};

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

  const averageBytes = stats?.objectCount
    ? stats.totalBytes / stats.objectCount
    : 0;
  const largestBucket = Math.max(
    1,
    ...(stats?.objectSizeHistogram.map((bucket) => bucket.count) ?? [])
  );

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

        {stats?.ready ? (
          <>
            <div className="grid grid-cols-2 border-b border-border lg:grid-cols-4">
              <Stat label="Objects" value={compact(stats.objectCount)} />
              <Stat label="Stored data" value={formatBytes(stats.totalBytes)} />
              <Stat label="Average object" value={formatBytes(averageBytes)} />
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
            <div className="divide-y divide-border">
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
                  <span className="text-right">{compact(bucket.count)}</span>
                </div>
              ))}
            </div>
          </>
        ) : (
          <div className="grid min-h-52 place-items-center border-b border-border px-6 text-center text-[10px] text-muted-foreground">
            <div>
              <p className="text-foreground">
                {loading ? "Loading storage stats…" : "Calculating inventory…"}
              </p>
              <p className="mt-2 max-w-md leading-5">
                The first usage snapshot is being built in the background. This
                page will not wait for the full object scan.
              </p>
            </div>
          </div>
        )}

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
