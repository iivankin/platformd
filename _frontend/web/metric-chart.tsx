import { memo } from "react";

import type { ResourceUsageHistory } from "@/api";

type MetricPoint = ResourceUsageHistory["points"][number];

export interface MetricSeries {
  color: string;
  label: string;
  value: (point: MetricPoint) => number | undefined;
}

interface MetricChartProps {
  emptyLabel: string;
  formatValue: (value: number) => string;
  from: number;
  minimumMaximum: number;
  points: MetricPoint[];
  series: MetricSeries[];
  title: string;
  to: number;
}

const gridTicks = [0, 0.25, 0.5, 0.75, 1];

const niceCeiling = (value: number) => {
  if (value <= 0) {
    return 1;
  }
  const magnitude = 10 ** Math.floor(Math.log10(value));
  const normalized = value / magnitude;
  let step = 10;
  if (normalized <= 1) {
    step = 1;
  } else if (normalized <= 2) {
    step = 2;
  } else if (normalized <= 5) {
    step = 5;
  }
  return step * magnitude;
};

const pathForSeries = (
  points: MetricPoint[],
  value: MetricSeries["value"],
  from: number,
  to: number,
  maximum: number
) => {
  const duration = Math.max(1, to - from);
  let connected = false;
  let path = "";
  for (const point of points) {
    const metric = value(point);
    if (metric === undefined) {
      connected = false;
      continue;
    }
    const x = Math.max(
      0,
      Math.min(100, ((point.observedAt - from) / duration) * 100)
    );
    const y = 100 - (Math.max(0, metric) / maximum) * 100;
    path += `${connected ? "L" : "M"}${x.toFixed(2)},${y.toFixed(2)}`;
    connected = true;
  }
  return path;
};

const timelineLabel = (timestamp: number, duration: number) => {
  const date = new Date(timestamp);
  if (duration <= 24 * 60 * 60_000) {
    return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  if (duration <= 7 * 24 * 60 * 60_000) {
    return date.toLocaleDateString([], {
      day: "numeric",
      hour: "2-digit",
      month: "short",
    });
  }
  return date.toLocaleDateString([], { day: "numeric", month: "short" });
};

const latestValue = (points: MetricPoint[], series: MetricSeries) => {
  for (let index = points.length - 1; index >= 0; index -= 1) {
    const point = points[index];
    if (point) {
      const value = series.value(point);
      if (value !== undefined) {
        return value;
      }
    }
  }
};

const MetricChartComponent = ({
  emptyLabel,
  formatValue,
  from,
  minimumMaximum,
  points,
  series,
  title,
  to,
}: MetricChartProps) => {
  let highest = 0;
  for (const point of points) {
    for (const metric of series) {
      highest = Math.max(highest, metric.value(point) ?? 0);
    }
  }
  const maximum = niceCeiling(Math.max(highest * 1.1, minimumMaximum));
  const duration = Math.max(0, to - from);
  const hasValues =
    highest > 0 ||
    points.some((point) => series.some((metric) => metric.value(point) === 0));

  return (
    <section className="min-w-0">
      <header className="flex min-h-12 flex-wrap items-center gap-x-4 gap-y-1 border-b border-border px-4 py-2.5">
        <h3 className="mr-auto text-[11px] font-medium">{title}</h3>
        {series.map((metric) => {
          const value = latestValue(points, metric);
          return (
            <span
              className="flex items-center gap-1.5 text-[9px] text-muted-foreground"
              key={metric.label}
            >
              <span
                className="size-1.5"
                style={{ backgroundColor: metric.color }}
              />
              {metric.label}
              <span className="text-foreground">
                {value === undefined ? "—" : formatValue(value)}
              </span>
            </span>
          );
        })}
      </header>
      <div className="relative h-56 px-3 py-3">
        <div className="absolute top-3 bottom-7 left-2 w-14 text-[8px] text-muted-foreground">
          {gridTicks.map((tick) => (
            <span
              className="absolute right-1 -translate-y-1/2"
              key={tick}
              style={{ top: `${(1 - tick) * 100}%` }}
            >
              {formatValue(maximum * tick)}
            </span>
          ))}
        </div>
        <div className="absolute top-3 right-3 bottom-7 left-16 border-b border-l border-border">
          <svg
            className="size-full overflow-visible"
            preserveAspectRatio="none"
            viewBox="0 0 100 100"
          >
            <title>{`${title} history`}</title>
            {gridTicks.slice(1).map((tick) => (
              <line
                className="stroke-border"
                key={`horizontal-${tick}`}
                strokeDasharray="1 2"
                strokeWidth="0.5"
                vectorEffect="non-scaling-stroke"
                x1="0"
                x2="100"
                y1={(1 - tick) * 100}
                y2={(1 - tick) * 100}
              />
            ))}
            {gridTicks.slice(1, -1).map((tick) => (
              <line
                className="stroke-border/60"
                key={`vertical-${tick}`}
                strokeWidth="0.5"
                vectorEffect="non-scaling-stroke"
                x1={tick * 100}
                x2={tick * 100}
                y1="0"
                y2="100"
              />
            ))}
            {series.map((metric) => (
              <path
                d={pathForSeries(points, metric.value, from, to, maximum)}
                fill="none"
                key={metric.label}
                stroke={metric.color}
                strokeLinecap="square"
                strokeLinejoin="miter"
                strokeWidth="1.5"
                vectorEffect="non-scaling-stroke"
              />
            ))}
          </svg>
          {hasValues ? null : (
            <p className="absolute inset-0 grid place-items-center text-[9px] text-muted-foreground">
              {emptyLabel}
            </p>
          )}
        </div>
        <div className="absolute right-3 bottom-1 left-16 flex justify-between text-[8px] text-muted-foreground">
          {duration > 0
            ? gridTicks.map((tick) => (
                <span key={tick}>
                  {timelineLabel(from + duration * tick, duration)}
                </span>
              ))
            : null}
        </div>
      </div>
    </section>
  );
};

export const MetricChart = memo(MetricChartComponent);
