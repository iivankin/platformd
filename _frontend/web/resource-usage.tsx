import { Activity, AlertTriangle, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { fetchResourceUsage } from "@/api";
import type { ResourceUsage as Usage, ResourceUsageKind } from "@/api";

const refreshMillis = 5000;

const formatBytes = (value: number) => {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

export const cpuMillicoresBetween = (
  previous: Usage,
  current: Usage
): number | undefined => {
  const elapsedMillis = current.observedAt - previous.observedAt;
  const reset = current.cpuUsageMicros < previous.cpuUsageMicros;
  if (elapsedMillis <= 0 || reset || !(previous.running && current.running)) {
    return undefined;
  }
  return Math.round(
    (current.cpuUsageMicros - previous.cpuUsageMicros) / elapsedMillis
  );
};

const Metric = ({
  detail,
  label,
  value,
}: {
  detail: string;
  label: string;
  value: string;
}) => (
  <div className="border-b border-border px-4 py-3 sm:border-r sm:border-b-0 sm:last:border-r-0">
    <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
      {label}
    </p>
    <p className="mt-1 text-[10px]">{value}</p>
    <p className="mt-1 text-[9px] text-muted-foreground">{detail}</p>
  </div>
);

export const ResourceUsage = ({
  cpuMillicores,
  kind,
  memoryBytes,
  resourceID,
}: {
  cpuMillicores?: number;
  kind: ResourceUsageKind;
  memoryBytes?: number;
  resourceID: string;
}) => {
  const previous = useRef<Usage | null>(null);
  const inFlight = useRef(false);
  const [usage, setUsage] = useState<Usage | null>(null);
  const [actualCPU, setActualCPU] = useState<number>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      if (inFlight.current) {
        return;
      }
      inFlight.current = true;
      try {
        const current = await fetchResourceUsage(
          kind,
          resourceID,
          controller.signal
        );
        setActualCPU(
          previous.current
            ? cpuMillicoresBetween(previous.current, current)
            : undefined
        );
        previous.current = current;
        setUsage(current);
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
            : "Unable to read resource usage"
        );
      } finally {
        inFlight.current = false;
      }
    };
    void load();
    const interval = globalThis.setInterval(() => void load(), refreshMillis);
    return () => {
      controller.abort();
      globalThis.clearInterval(interval);
      previous.current = null;
    };
  }, [kind, resourceID]);

  let state = "Reading current counters…";
  if (usage) {
    state = usage.running ? "Live cgroup" : "Stopped";
  }
  const actualMemory = usage?.running ? formatBytes(usage.memoryBytes) : "—";
  let cpu = "—";
  if (usage?.running) {
    cpu =
      actualCPU === undefined ? "Sampling…" : `${actualCPU.toLocaleString()}m`;
  }

  return (
    <section className="border-b border-border">
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5 text-[9px] text-muted-foreground">
        {usage || error ? (
          <Activity className="size-3" />
        ) : (
          <LoaderCircle className="size-3 animate-spin" />
        )}
        <span className="tracking-[0.12em] uppercase">Resource usage</span>
        <span className="ml-auto">{error ?? state}</span>
      </div>
      <div className="grid sm:grid-cols-3">
        <Metric
          detail={`Limit ${cpuMillicores ? `${cpuMillicores.toLocaleString()}m` : "unlimited"}`}
          label="CPU now"
          value={cpu}
        />
        <Metric
          detail={`Limit ${memoryBytes ? formatBytes(memoryBytes) : "unlimited"}`}
          label="Memory now"
          value={actualMemory}
        />
        <Metric
          detail={
            usage
              ? `${usage.hostCpuCores.toLocaleString()} vCPU`
              : "Reading capacity…"
          }
          label="Host capacity"
          value={usage ? formatBytes(usage.hostMemoryBytes) : "—"}
        />
      </div>
      <p className="flex items-start gap-2 px-4 py-2.5 text-[9px] leading-4 text-amber-700 dark:text-amber-400">
        <AlertTriangle className="mt-0.5 size-3 shrink-0" />
        Hard limits are not reservations. Configured totals may exceed host
        capacity.
      </p>
    </section>
  );
};
