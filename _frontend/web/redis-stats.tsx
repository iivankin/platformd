import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { fetchManagedRedisStats } from "@/api";
import type { ManagedRedisStats } from "@/api";
import { Button } from "@/components/ui/button";
import { formatBytes, formatTTL } from "@/redis-data-utils";

const compact = (value: number) =>
  Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
    notation: "compact",
  }).format(value);

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
  redisID,
}: {
  projectID: string;
  redisID: string;
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
    const loadInitialStats = async () => {
      try {
        const loaded = await fetchManagedRedisStats(projectID, redisID);
        setStats(loaded);
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
    };
    void loadInitialStats();
  }, [projectID, redisID]);

  const hitRate = useMemo(() => {
    if (!stats) {
      return 0;
    }
    const total = stats.keyspaceHits + stats.keyspaceMisses;
    return total === 0 ? 0 : (stats.keyspaceHits / total) * 100;
  }, [stats]);

  if (!stats) {
    return (
      <div className="grid min-h-52 place-items-center border-b border-border px-6 text-[10px] text-muted-foreground">
        {error ?? (loading ? "Loading Redis stats…" : "Stats unavailable")}
      </div>
    );
  }

  return (
    <section>
      <header className="flex items-center justify-between border-b border-border px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">Live Redis statistics</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Read directly from INFO; refreshes on demand.
          </p>
        </div>
        <Button
          disabled={loading}
          onClick={() => void load()}
          size="icon"
          variant="ghost"
        >
          <RefreshCw />
        </Button>
      </header>
      <div className="grid grid-cols-3 border-b border-border lg:grid-cols-6">
        <Stat label="Version" value={stats.version} />
        <Stat label="Uptime" value={formatTTL(stats.uptimeSeconds * 1000)} />
        <Stat label="Clients" value={compact(stats.connectedClients)} />
        <Stat
          label="Blocked"
          value={compact(stats.blockedClients)}
          warning={stats.blockedClients > 0}
        />
        <Stat
          label="Rejected"
          value={compact(stats.rejectedConnections)}
          warning={stats.rejectedConnections > 0}
        />
        <Stat label="Ops/sec" value={compact(stats.operationsPerSecond)} />
      </div>
      <div className="grid grid-cols-3 border-b border-border lg:grid-cols-6">
        <Stat label="Used memory" value={formatBytes(stats.usedMemoryBytes)} />
        <Stat label="RSS memory" value={formatBytes(stats.rssMemoryBytes)} />
        <Stat label="Peak memory" value={formatBytes(stats.peakMemoryBytes)} />
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
              : formatBytes(stats.maxMemoryBytes)
          }
        />
        <Stat
          label="Hit rate"
          value={`${hitRate.toFixed(1)}%`}
          warning={hitRate < 80}
        />
      </div>
      <header className="border-b border-border px-5 py-3 text-[10px] font-medium">
        Command stats
      </header>
      <table className="w-full border-collapse text-left text-[10px]">
        <thead>
          <tr className="border-b border-border">
            <th className="px-5 py-2 font-medium">Command</th>
            <th className="px-5 py-2 text-right font-medium">Calls</th>
            <th className="px-5 py-2 text-right font-medium">Avg latency</th>
            <th className="px-5 py-2 text-right font-medium">Total time</th>
          </tr>
        </thead>
        <tbody>
          {stats.commands.slice(0, 30).map((command) => (
            <tr className="border-b border-border" key={command.name}>
              <td className="px-5 py-2 font-mono uppercase">{command.name}</td>
              <td className="px-5 py-2 text-right">{compact(command.calls)}</td>
              <td className="px-5 py-2 text-right">
                {command.microsPerCall.toFixed(2)}µs
              </td>
              <td className="px-5 py-2 text-right">
                {(command.totalMicros / 1_000_000).toFixed(2)}s
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <header className="border-b border-border px-5 py-3 text-[10px] font-medium">
        Keyspaces
      </header>
      {stats.keyspaces.map((keyspace) => (
        <div
          className="grid grid-cols-4 border-b border-border px-5 py-2 text-[10px]"
          key={keyspace.database}
        >
          <code>{keyspace.database}</code>
          <span>{compact(keyspace.keys)} keys</span>
          <span>{compact(keyspace.expires)} expire</span>
          <span className="text-right">
            avg TTL {formatTTL(keyspace.averageTtlMillis)}
          </span>
        </div>
      ))}
      {error ? (
        <p className="border-b border-border px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </section>
  );
};
