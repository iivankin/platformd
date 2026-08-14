import { cn } from "@/lib/utils";
import { formatTelemetryRange } from "@/telemetry-time-range";

export interface TelemetryHistogramPoint {
  error?: boolean;
  timestamp: number;
}

interface HistogramBin {
  errors: number;
  from: number;
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
  const bins = Array.from({ length: binCount }, (_, index) => ({
    errors: 0,
    from: Math.floor(start + index * width),
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
  onSelectRange,
  points,
}: {
  ariaLabel: string;
  bounds?: { from?: number; to?: number };
  noun: string;
  onSelectRange: (from: number, to: number) => void;
  points: TelemetryHistogramPoint[];
}) => {
  const bins = telemetryHistogramBins(points, bounds);
  const maximum = Math.max(1, ...bins.map((bin) => bin.total));
  const [first] = bins;
  const last = bins.at(-1);
  const showSeconds = Boolean(first && last && last.to - first.from < 60_000);

  return (
    <fieldset className="relative min-w-0 border-0 border-b border-border px-4 pt-6">
      <legend className="sr-only">{ariaLabel}</legend>
      {first && last ? (
        <div className="pointer-events-none absolute inset-x-4 top-2 flex justify-between text-[8px] text-muted-foreground tabular-nums">
          <time>{histogramTick(first.from, showSeconds)}</time>
          <time>{histogramTick(last.to, showSeconds)}</time>
        </div>
      ) : null}
      <div className="flex h-16 items-end gap-px">
        {bins.map((bin, index) => {
          const label = `${bin.total.toLocaleString()} ${noun} · ${formatTelemetryRange(bin.from, bin.to)}`;
          return (
            <button
              aria-label={`Jump to ${label}`}
              className="group relative flex h-full min-w-0 flex-1 cursor-crosshair items-end focus-visible:z-10 focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-none"
              key={`${index.toString()}:${bin.from.toString()}`}
              onClick={() => onSelectRange(bin.from, bin.to)}
              title={label}
              type="button"
            >
              <span
                className={cn(
                  "block w-full transition-colors",
                  bin.total > 0
                    ? "bg-sky-500/65 group-hover:bg-sky-400"
                    : "h-px bg-border group-hover:bg-sky-400/60"
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
    </fieldset>
  );
};
