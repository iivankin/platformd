import { useEffect, useMemo, useState } from "react";

import type {
  ManagedStatsHistory,
  ManagedStatsHistoryPoint,
  ResourceUsageRange,
} from "@/api";
import { MetricChart } from "@/metric-chart";
import type { MetricSeries } from "@/metric-chart";

const ranges: { label: string; value: ResourceUsageRange }[] = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "1d", value: "1d" },
  { label: "7d", value: "7d" },
  { label: "30d", value: "30d" },
];

export const managedStatsChartColors = {
  danger: "#fb7185",
  primary: "#38bdf8",
  secondary: "#fbbf24",
  tertiary: "#34d399",
} as const;

export const managedStatsBreakdownColors = [
  managedStatsChartColors.primary,
  managedStatsChartColors.secondary,
  managedStatsChartColors.tertiary,
  managedStatsChartColors.danger,
  "#a78bfa",
  "#f472b6",
  "#22d3ee",
  "#94a3b8",
] as const;

export const objectStoreOperationsBreakdown = [
  { key: "getOperationsPerSecond", label: "Get" },
  { key: "putOperationsPerSecond", label: "Put" },
  { key: "deleteOperationsPerSecond", label: "Delete" },
  { key: "listOperationsPerSecond", label: "List" },
  { key: "otherOperationsPerSecond", label: "Other" },
] as const;

const emptyPoints: ManagedStatsHistoryPoint[] = [];

export const formatCompact = (value: number) =>
  Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
    notation: "compact",
  }).format(value);

export const formatStatsBytes = (value: number) => {
  if (!Number.isFinite(value) || value === 0) {
    return "0 B";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const unit = Math.min(
    Math.max(0, Math.floor(Math.log(Math.abs(value)) / Math.log(1024))),
    units.length - 1
  );
  const precision = unit === 0 && Number.isInteger(value) ? 0 : 1;
  return `${(value / 1024 ** unit).toFixed(precision)} ${units[unit]}`;
};

export const formatStatsRate = (value: number) =>
  `${formatStatsBytes(value)}/s`;

export const formatMicros = (value: number) => {
  if (value >= 1000) {
    return `${(value / 1000).toFixed(value >= 10_000 ? 0 : 1)} ms`;
  }
  return `${value.toFixed(value >= 100 ? 0 : 1)} µs`;
};

export const formatMillis = (value: number) =>
  `${value.toFixed(value >= 100 ? 0 : 1)} ms`;

export const metricNumber = (
  metrics: Record<string, unknown>,
  key: string
): number | undefined => {
  const value = metrics[key];
  return typeof value === "number" && Number.isFinite(value)
    ? value
    : undefined;
};

export const operationsBreakdownKeys = ({
  fixed,
  history,
  limit = 8,
  otherKey,
  otherLabel = "Other",
  prefix,
}: {
  fixed?: readonly { key: string; label: string }[];
  history: ManagedStatsHistory | null;
  limit?: number;
  otherKey?: string;
  otherLabel?: string;
  prefix?: string;
}): { color: string; key: string; label: string }[] => {
  if (fixed) {
    return fixed.map((item, index) => ({
      color:
        managedStatsBreakdownColors[
          index % managedStatsBreakdownColors.length
        ] ?? managedStatsChartColors.primary,
      key: item.key,
      label: item.label,
    }));
  }
  if (!(history && prefix)) {
    return otherKey
      ? [
          {
            color: managedStatsBreakdownColors[0],
            key: otherKey,
            label: otherLabel,
          },
        ]
      : [];
  }
  const totals = new Map<string, number>();
  for (const point of history.points) {
    for (const [key, value] of Object.entries(point.metrics)) {
      if (!key.startsWith(prefix) || typeof value !== "number") {
        continue;
      }
      totals.set(key, (totals.get(key) ?? 0) + value);
    }
  }
  const ranked = [...totals.entries()]
    .toSorted(
      (left, right) => right[1] - left[1] || left[0].localeCompare(right[0])
    )
    .slice(0, Math.max(0, otherKey ? limit - 1 : limit));
  const keys = ranked.map(([key], index) => ({
    color:
      managedStatsBreakdownColors[index % managedStatsBreakdownColors.length] ??
      managedStatsChartColors.primary,
    key,
    label: key.slice(prefix.length),
  }));
  if (otherKey) {
    keys.push({
      color:
        managedStatsBreakdownColors[
          keys.length % managedStatsBreakdownColors.length
        ] ?? managedStatsChartColors.primary,
      key: otherKey,
      label: otherLabel,
    });
  }
  return keys;
};

/** Fold cmd.* rates not in the selected series into Other so volume is conserved. */
export const foldUnselectedOperationsIntoOther = (
  history: ManagedStatsHistory | null,
  selectedKeys: readonly { key: string }[],
  {
    otherKey,
    prefix,
  }: {
    otherKey: string;
    prefix: string;
  }
): ManagedStatsHistory | null => {
  if (!history) {
    return null;
  }
  const selected = new Set(
    selectedKeys.map((item) => item.key).filter((key) => key.startsWith(prefix))
  );
  return {
    ...history,
    points: history.points.map((point) => {
      let other = metricNumber(point.metrics, otherKey) ?? 0;
      const metrics: Record<string, unknown> = {};
      for (const [key, value] of Object.entries(point.metrics)) {
        if (key.startsWith(prefix) && typeof value === "number") {
          if (selected.has(key)) {
            metrics[key] = value;
            continue;
          }
          other += value;
          continue;
        }
        metrics[key] = value;
      }
      metrics[otherKey] = other;
      return { ...point, metrics };
    }),
  };
};

const historyStatusLabel = (
  history: ManagedStatsHistory | null,
  historyError?: string
) => {
  if (historyError) {
    return historyError;
  }
  if (history) {
    return `${history.points.length} samples`;
  }
  return "Loading…";
};

export const ManagedStatsRangePicker = ({
  history,
  historyError,
  onChange,
  range,
}: {
  history: ManagedStatsHistory | null;
  historyError?: string;
  onChange: (range: ResourceUsageRange) => void;
  range: ResourceUsageRange;
}) => (
  <div className="flex items-center gap-1 border-b border-border px-4 py-2.5">
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
      {historyStatusLabel(history, historyError)}
    </span>
  </div>
);

export const useManagedStatsHistory = (
  fetchHistory: (
    range: ResourceUsageRange,
    signal: AbortSignal
  ) => Promise<ManagedStatsHistory>
) => {
  const [range, setRange] = useState<ResourceUsageRange>("1h");
  const [history, setHistory] = useState<ManagedStatsHistory | null>(null);
  const [loadedRange, setLoadedRange] = useState<ResourceUsageRange | null>(
    null
  );
  const [historyError, setHistoryError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const run = async () => {
      try {
        const next = await fetchHistory(range, controller.signal);
        setHistory(next);
        setLoadedRange(range);
        setHistoryError(undefined);
      } catch (error) {
        if (error instanceof DOMException && error.name === "AbortError") {
          return;
        }
        setHistory(null);
        setLoadedRange(range);
        setHistoryError(
          error instanceof Error ? error.message : "Unable to load history"
        );
      }
    };
    void run();
    return () => controller.abort();
  }, [fetchHistory, range]);

  return {
    history: loadedRange === range ? history : null,
    historyError: loadedRange === range ? historyError : undefined,
    range,
    setRange,
  };
};

export const ManagedMetricChart = ({
  emptyLabel,
  formatValue,
  history,
  keys,
  minimumMaximum,
  title,
}: {
  emptyLabel: string;
  formatValue: (value: number) => string;
  history: ManagedStatsHistory | null;
  keys: {
    color: string;
    key: string;
    label: string;
    strokeDasharray?: string;
  }[];
  minimumMaximum: number;
  title: string;
}) => {
  const points = history?.points ?? emptyPoints;
  const series = useMemo<MetricSeries<ManagedStatsHistoryPoint>[]>(
    () =>
      keys.map((item) => ({
        color: item.color,
        label: item.label,
        strokeDasharray: item.strokeDasharray,
        value: (point) => metricNumber(point.metrics, item.key),
      })),
    [keys]
  );
  return (
    <MetricChart
      emptyLabel={emptyLabel}
      formatValue={formatValue}
      from={history?.from ?? 0}
      minimumMaximum={minimumMaximum}
      points={points}
      series={series}
      title={title}
      to={history?.to ?? 0}
    />
  );
};

export const managedHistoryEmptyLabel = (
  history: ManagedStatsHistory | null,
  historyError?: string
) => {
  if (historyError) {
    return historyError;
  }
  if (!history) {
    return "Loading history…";
  }
  if (history.points.length === 0) {
    return "No samples in this range yet";
  }
  return "No data";
};
