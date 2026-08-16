import type { FocusEvent, MouseEvent } from "react";
import { useRef, useState } from "react";

import { cn } from "@/lib/utils";
import { formatTelemetryRange } from "@/telemetry-time-range";

export interface TelemetryHistogramPoint {
  error?: boolean;
  id?: string;
  timestamp: number;
}

interface HistogramBin {
  errors: number;
  from: number;
  points: TelemetryHistogramPoint[];
  to: number;
  total: number;
}

const binCount = 28;

export const telemetryHistogramBins = (
  points: TelemetryHistogramPoint[],
  bounds?: { from?: number; to?: number }
): HistogramBin[] => {
  const timestamps = points
    .map((point) => point.timestamp)
    .filter(Number.isFinite);
  const dataStart =
    timestamps.length > 0 ? Math.min(...timestamps) : Date.now();
  const dataEnd = timestamps.length > 0 ? Math.max(...timestamps) : dataStart;
  const start = bounds?.from ?? dataStart;
  const end = Math.max(start + 1, bounds?.to ?? dataEnd);
  const width = (end - start) / binCount;
  const bins: HistogramBin[] = Array.from({ length: binCount }, (_, index) => ({
    errors: 0,
    from: Math.floor(start + index * width),
    points: [],
    to: Math.ceil(start + (index + 1) * width),
    total: 0,
  }));
  for (const point of points) {
    if (
      !Number.isFinite(point.timestamp) ||
      point.timestamp < start ||
      point.timestamp > end
    ) {
      continue;
    }
    const index = Math.min(
      binCount - 1,
      Math.floor((point.timestamp - start) / width)
    );
    const bin = bins[index];
    if (!bin) {
      continue;
    }
    bin.points.push(point);
    bin.total += 1;
    if (point.error) {
      bin.errors += 1;
    }
  }
  return bins;
};

const histogramTick = (value: number, showSeconds: boolean) =>
  new Date(value).toLocaleString([], {
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    month: "short",
    second: showSeconds ? "2-digit" : undefined,
  });

export const TelemetryHistogram = ({
  ariaLabel,
  bounds,
  noun,
  onJumpTo,
  points,
}: {
  ariaLabel: string;
  bounds?: { from?: number; to?: number };
  noun: string;
  onJumpTo: (point: TelemetryHistogramPoint) => void;
  points: TelemetryHistogramPoint[];
}) => {
  const containerRef = useRef<HTMLFieldSetElement>(null);
  const [activeIndex, setActiveIndex] = useState<number>();
  const [hover, setHover] = useState<{
    index: number;
    x: number;
    y: number;
  }>();
  const bins = telemetryHistogramBins(points, bounds);
  const maximum = Math.max(1, ...bins.map((bin) => bin.total));
  const [first] = bins;
  const last = bins.at(-1);
  const showSeconds = Boolean(first && last && last.to - first.from < 60_000);
  const hoveredBin = hover ? bins[hover.index] : undefined;

  const updateHover = (
    index: number,
    event: FocusEvent<HTMLButtonElement> | MouseEvent<HTMLButtonElement>
  ) => {
    const container = containerRef.current?.getBoundingClientRect();
    const target = event.currentTarget.getBoundingClientRect();
    if (!container) {
      return;
    }
    const isMouseEvent = "clientX" in event;
    setHover({
      index,
      x: isMouseEvent
        ? event.clientX - container.left
        : target.left - container.left + target.width / 2,
      y: isMouseEvent
        ? event.clientY - container.top
        : target.top - container.top,
    });
  };

  const jumpToBin = (bin: HistogramBin, index: number) => {
    const midpoint = bin.from + (bin.to - bin.from) / 2;
    let point: TelemetryHistogramPoint | undefined;
    for (const candidate of bin.points) {
      if (
        !point ||
        Math.abs(candidate.timestamp - midpoint) <
          Math.abs(point.timestamp - midpoint)
      ) {
        point = candidate;
      }
    }
    if (!point) {
      return;
    }
    setActiveIndex(index);
    onJumpTo(point);
  };

  return (
    <fieldset
      className="relative min-w-0 border-0 border-b border-border px-4 pt-6"
      ref={containerRef}
    >
      <legend className="sr-only">{ariaLabel}</legend>
      {first && last ? (
        <div className="pointer-events-none absolute inset-x-4 top-2 flex justify-between text-[8px] text-muted-foreground tabular-nums">
          <time>{histogramTick(first.from, showSeconds)}</time>
          <time>{histogramTick(last.to, showSeconds)}</time>
        </div>
      ) : null}
      <div className="flex h-16 items-end gap-px">
        {bins.map((bin, index) => {
          const rangeLabel = formatTelemetryRange(bin.from, bin.to);
          const countLabel = `${bin.total.toLocaleString()} ${
            bin.total === 1 ? noun.replace(/s$/u, "") : noun
          }`;
          const label = `${countLabel} · ${rangeLabel}`;
          const isHovered = hover?.index === index;
          return (
            <button
              aria-label={
                bin.total > 0
                  ? `Scroll to ${label}`
                  : `No ${noun} · ${rangeLabel}`
              }
              className={cn(
                "group relative flex h-full min-w-0 flex-1 items-end transition-opacity outline-none",
                bin.total > 0 ? "cursor-pointer" : "cursor-default",
                hover && !isHovered && "opacity-35"
              )}
              disabled={bin.total === 0}
              key={`${index.toString()}:${bin.from.toString()}`}
              onBlur={() => setHover(undefined)}
              onClick={() => jumpToBin(bin, index)}
              onFocus={(event) => updateHover(index, event)}
              onMouseEnter={(event) => updateHover(index, event)}
              onMouseLeave={() => setHover(undefined)}
              onMouseMove={(event) => updateHover(index, event)}
              type="button"
            >
              <span
                className={cn(
                  "pointer-events-none absolute -inset-x-px inset-y-0 bg-foreground/[0.035] opacity-0 transition-opacity",
                  (isHovered || activeIndex === index) && "opacity-100"
                )}
              />
              <span
                className={cn(
                  "relative block w-full transition-[background-color,opacity]",
                  bin.total > 0
                    ? "bg-sky-500/65 group-hover:bg-sky-400 group-focus-visible:bg-sky-400"
                    : "h-px bg-border",
                  activeIndex === index && "bg-sky-400"
                )}
                style={
                  bin.total > 0
                    ? { height: `${Math.max(3, (bin.total / maximum) * 100)}%` }
                    : undefined
                }
              >
                {bin.errors > 0 ? (
                  <span
                    className="block w-full bg-rose-500"
                    style={{ height: `${(bin.errors / bin.total) * 100}%` }}
                  />
                ) : null}
              </span>
            </button>
          );
        })}
      </div>
      {hoveredBin && hover ? (
        <div
          className="pointer-events-none absolute z-20 min-w-44 border border-border bg-popover px-2.5 py-2 text-[9px] text-popover-foreground shadow-lg"
          style={{
            left: `clamp(0.5rem, ${hover.x + 12}px, calc(100% - 12rem))`,
            top: Math.max(4, hover.y - 42),
          }}
        >
          <div className="flex items-center justify-between gap-6 tabular-nums">
            <span>{formatTelemetryRange(hoveredBin.from, hoveredBin.to)}</span>
            <strong className="font-medium">
              {hoveredBin.total.toLocaleString()}
            </strong>
          </div>
          {hoveredBin.errors > 0 ? (
            <div className="mt-1 flex items-center justify-between gap-6 text-rose-500 tabular-nums">
              <span>errors</span>
              <span>{hoveredBin.errors.toLocaleString()}</span>
            </div>
          ) : null}
          {hoveredBin.total > 0 ? (
            <p className="mt-1 text-[8px] text-muted-foreground">
              Click to scroll
            </p>
          ) : null}
        </div>
      ) : null}
    </fieldset>
  );
};
