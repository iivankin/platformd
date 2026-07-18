import { Activity, LoaderCircle } from "lucide-react";
import { useState } from "react";

import type {
  ResourceUsage as Usage,
  ResourceUsageHistory,
  ResourceUsageKind,
  ResourceUsageRange,
} from "@/api";
import { SectionCard } from "@/components/ui/card";
import { MetricChart } from "@/metric-chart";
import type { MetricSeries } from "@/metric-chart";
import {
  useCurrentResourceUsage,
  useResourceUsageHistory,
} from "@/use-resource-usage";

export {
  cpuMillicoresBetween,
  networkBytesPerSecondBetween,
} from "@/resource-usage-rates";

const emptyPoints: ResourceUsageHistory["points"] = [];

const ranges: { label: string; value: ResourceUsageRange }[] = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "1d", value: "1d" },
  { label: "7d", value: "7d" },
  { label: "30d", value: "30d" },
];

const cpuSeries: MetricSeries[] = [
  {
    color: "var(--chart-2)",
    label: "Usage",
    value: (point) => point.cpuMillicores,
  },
];

const memorySeries: MetricSeries[] = [
  {
    color: "var(--chart-1)",
    label: "Working set",
    value: (point) => point.memoryBytes,
  },
];

const networkSeries: MetricSeries[] = [
  {
    color: "var(--chart-3)",
    label: "Ingress",
    value: (point) => point.networkIngressBytesPerSecond,
  },
  {
    color: "var(--chart-1)",
    label: "Egress",
    value: (point) => point.networkEgressBytesPerSecond,
  },
];

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

const formatRate = (value: number) => `${formatBytes(value)}/s`;

const formatMillicores = (value: number) =>
  value >= 1000 ? `${(value / 1000).toFixed(1)} vCPU` : `${Math.round(value)}m`;

const statusFor = (usage: Usage | null) => {
  if (!usage) {
    return "Reading current usage…";
  }
  return usage.running ? "Live" : "Stopped";
};

const cpuValueFor = (usage: Usage | null, cpuMillicores?: number) => {
  if (!usage?.running) {
    return "—";
  }
  return cpuMillicores === undefined
    ? "Sampling…"
    : formatMillicores(cpuMillicores);
};

const networkValueFor = (
  usage: Usage | null,
  network?: { egress: number; ingress: number }
) => {
  if (network) {
    return `${formatRate(network.ingress)} ↓  ${formatRate(network.egress)} ↑`;
  }
  return usage?.running && usage.networkAvailable ? "Sampling…" : "—";
};

const emptyLabelFor = (
  history: ResourceUsageHistory | null,
  error?: string
) => {
  if (error) {
    return error;
  }
  return history ? "Collecting samples…" : "Loading history…";
};

const historyStatusFor = (
  history: ResourceUsageHistory | null,
  error?: string
) => {
  if (error) {
    return error;
  }
  return history ? `${history.points.length} samples` : "Loading…";
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

const UsageHeader = ({
  error,
  loading,
  state,
}: {
  error?: string;
  loading: boolean;
  state: string;
}) => (
  <div className="flex min-h-11 flex-wrap items-center gap-2 border-b border-border px-4 py-2.5 text-[9px] text-muted-foreground">
    {loading ? (
      <LoaderCircle className="size-3 animate-spin" />
    ) : (
      <Activity className="size-3" />
    )}
    <span className="tracking-[0.12em] uppercase">Resource usage</span>
    <span className="ml-auto">{error ?? state}</span>
  </div>
);

const UsageSummary = ({
  actualCPU,
  actualNetwork,
  cpuLimit,
  memoryLimit,
  usage,
}: {
  actualCPU?: number;
  actualNetwork?: { egress: number; ingress: number };
  cpuLimit?: number;
  memoryLimit?: number;
  usage: Usage | null;
}) => {
  const actualMemory = usage?.running ? formatBytes(usage.memoryBytes) : "—";
  return (
    <div className="grid sm:grid-cols-2 lg:grid-cols-4">
      <Metric
        detail={`Limit ${cpuLimit ? `${cpuLimit.toLocaleString()}m` : "unlimited"}`}
        label="CPU now"
        value={cpuValueFor(usage, actualCPU)}
      />
      <Metric
        detail={`Limit ${memoryLimit ? formatBytes(memoryLimit) : "unlimited"}`}
        label="Memory now"
        value={actualMemory}
      />
      <Metric
        detail="Ingress ↓  Egress ↑"
        label="Network now"
        value={networkValueFor(usage, actualNetwork)}
      />
      <Metric
        detail={
          usage ? `${usage.hostCpuCores.toLocaleString()} vCPU` : "Reading…"
        }
        label="Host capacity"
        value={usage ? formatBytes(usage.hostMemoryBytes) : "—"}
      />
    </div>
  );
};

const RangeSelector = ({
  history,
  historyError,
  onChange,
  range,
}: {
  history: ResourceUsageHistory | null;
  historyError?: string;
  onChange: (range: ResourceUsageRange) => void;
  range: ResourceUsageRange;
}) => (
  <div className="flex items-center gap-1 border-t border-border px-4 py-2.5">
    <span className="mr-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
      Range
    </span>
    {ranges.map((option) => (
      <button
        className={`h-7 border px-2.5 text-[9px] transition-colors ${
          range === option.value
            ? "border-foreground bg-foreground text-background"
            : "border-border text-muted-foreground hover:bg-muted hover:text-foreground"
        }`}
        key={option.value}
        onClick={() => onChange(option.value)}
        type="button"
      >
        {option.label}
      </button>
    ))}
    <span className="ml-auto text-[8px] text-muted-foreground">
      {historyStatusFor(history, historyError)}
    </span>
  </div>
);

const UsageCharts = ({
  history,
  historyError,
}: {
  history: ResourceUsageHistory | null;
  historyError?: string;
}) => {
  const points = history?.points ?? emptyPoints;
  const emptyLabel = emptyLabelFor(history, historyError);
  const from = history?.from ?? 0;
  const to = history?.to ?? 0;
  return (
    <div className="grid border-t border-border lg:grid-cols-2">
      <div className="min-w-0 lg:border-r lg:border-border">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatMillicores}
          from={from}
          minimumMaximum={100}
          points={points}
          series={cpuSeries}
          title="CPU"
          to={to}
        />
      </div>
      <div className="min-w-0 border-t border-border lg:border-t-0">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatBytes}
          from={from}
          minimumMaximum={1024 ** 2}
          points={points}
          series={memorySeries}
          title="Memory"
          to={to}
        />
      </div>
      <div className="min-w-0 border-t border-border lg:col-span-2">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatRate}
          from={from}
          minimumMaximum={1024}
          points={points}
          series={networkSeries}
          title="Network traffic"
          to={to}
        />
      </div>
    </div>
  );
};

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
  const [range, setRange] = useState<ResourceUsageRange>("1h");
  const {
    cpuMillicores: actualCPU,
    error: currentError,
    network: actualNetwork,
    usage,
  } = useCurrentResourceUsage(kind, resourceID);
  const { error: historyError, history } = useResourceUsageHistory(
    kind,
    resourceID,
    range
  );

  return (
    <SectionCard>
      <UsageHeader
        error={currentError}
        loading={!usage && !currentError}
        state={statusFor(usage)}
      />
      <UsageSummary
        actualCPU={actualCPU}
        actualNetwork={actualNetwork}
        cpuLimit={cpuMillicores}
        memoryLimit={memoryBytes}
        usage={usage}
      />
      <RangeSelector
        history={history}
        historyError={historyError}
        onChange={setRange}
        range={range}
      />
      <UsageCharts history={history} historyError={historyError} />
    </SectionCard>
  );
};
