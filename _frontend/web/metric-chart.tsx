import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  CSSProperties,
  PointerEvent as ReactPointerEvent,
  ReactElement,
} from "react";

export interface MetricPoint {
  observedAt: number;
}

export interface MetricSeries<T extends MetricPoint = MetricPoint> {
  color: string;
  label: string;
  points?: T[];
  strokeDasharray?: string;
  value: (point: T) => number | undefined;
}

interface MetricChartProps<T extends MetricPoint = MetricPoint> {
  emptyLabel: string;
  formatValue: (value: number) => string;
  from: number;
  minimumMaximum: number;
  points: T[];
  series: MetricSeries<T>[];
  title: string;
  to: number;
}

interface ChartCoordinate {
  pointIndex: number;
  x: number;
  y: number;
}

interface SeriesGeometry<T extends MetricPoint = MetricPoint> {
  areas: string[];
  coordinates: (ChartCoordinate | undefined)[];
  lines: string[];
  metric: MetricSeries<T>;
}

interface HoverState {
  pointIndex: number;
  pointerX: number;
  pointerY: number;
}

const chartHeight = 224;
const padding = { bottom: 24, right: 12, top: 12 } as const;
const minimumLeftPadding = 40;
const yLabelCharacterWidth = 5.5;
const tooltipWidth = 208;

const niceMaximum = (value: number) => {
  if (value <= 0) {
    return 5;
  }
  if (value <= 5) {
    return 5;
  }
  if (value <= 10) {
    return 10;
  }
  const magnitude = 10 ** Math.floor(Math.log10(value));
  const normalized = value / magnitude;
  if (normalized <= 1.5) {
    return Math.ceil(1.5 * magnitude);
  }
  if (normalized <= 2) {
    return 2 * magnitude;
  }
  if (normalized <= 3) {
    return 3 * magnitude;
  }
  if (normalized <= 5) {
    return 5 * magnitude;
  }
  return 10 * magnitude;
};

const timelineLabel = (timestamp: number, duration: number) => {
  const date = new Date(timestamp);
  if (duration >= 7 * 24 * 60 * 60_000) {
    return date.toLocaleDateString("en-US", {
      day: "numeric",
      month: "short",
    });
  }
  return date.toLocaleTimeString("en-US", {
    hour: "2-digit",
    hourCycle: "h23",
    minute: "2-digit",
  });
};

const timeLabelAnchor = (index: number, lastIndex: number) => {
  if (index === 0) {
    return "start";
  }
  if (index === lastIndex) {
    return "end";
  }
  return "middle";
};

const latestValue = <T extends MetricPoint>(
  points: T[],
  metric: MetricSeries<T>
) => {
  for (let index = points.length - 1; index >= 0; index -= 1) {
    const point = points[index];
    if (point) {
      const value = metric.value(point);
      if (value !== undefined) {
        return value;
      }
    }
  }
};

const linePath = (coordinates: ChartCoordinate[]) =>
  coordinates
    .map(({ x, y }, index) => `${index === 0 ? "M" : "L"} ${x} ${y}`)
    .join(" ");

const areaPath = (coordinates: ChartCoordinate[], baseline: number) => {
  const first = coordinates.at(0);
  const last = coordinates.at(-1);
  if (!(first && last)) {
    return "";
  }
  return `${linePath(coordinates)} L ${last.x} ${baseline} L ${first.x} ${baseline} Z`;
};

const geometryForSeries = <T extends MetricPoint>({
  maximum,
  metric,
  plotLeft,
  plotHeight,
  plotWidth,
  points,
}: {
  maximum: number;
  metric: MetricSeries<T>;
  plotLeft: number;
  plotHeight: number;
  plotWidth: number;
  points: T[];
}): SeriesGeometry<T> => {
  const baseline = padding.top + plotHeight;
  const xStep = points.length > 1 ? plotWidth / (points.length - 1) : plotWidth;
  const coordinates: SeriesGeometry["coordinates"] = Array.from({
    length: points.length,
  });
  const segments: ChartCoordinate[][] = [];
  let segment: ChartCoordinate[] = [];

  for (const [pointIndex, point] of points.entries()) {
    const value = metric.value(point);
    if (value === undefined || !Number.isFinite(value)) {
      if (segment.length > 0) {
        segments.push(segment);
        segment = [];
      }
      continue;
    }
    const coordinate = {
      pointIndex,
      x: plotLeft + pointIndex * xStep,
      y: padding.top + plotHeight - (Math.max(0, value) / maximum) * plotHeight,
    };
    coordinates[pointIndex] = coordinate;
    segment.push(coordinate);
  }
  if (segment.length > 0) {
    segments.push(segment);
  }

  return {
    areas: segments.map((segmentCoordinates) =>
      areaPath(segmentCoordinates, baseline)
    ),
    coordinates,
    lines: segments.map(linePath),
    metric,
  };
};

const AnimatedMetricLine = <T extends MetricPoint>({
  d,
  metric,
}: {
  d: string;
  metric: MetricSeries<T>;
}) => {
  const pathRef = useRef<SVGPathElement>(null);
  const [length, setLength] = useState<number>();

  useEffect(() => {
    const nextLength = pathRef.current?.getTotalLength();
    if (nextLength !== undefined) {
      setLength(nextLength);
    }
  }, [d]);

  const ready = length !== undefined;
  return (
    <path
      className={
        ready && !metric.strokeDasharray ? "metric-chart-line" : undefined
      }
      d={d}
      fill="none"
      ref={pathRef}
      stroke={metric.color}
      strokeDasharray={metric.strokeDasharray ?? length ?? 99_999}
      strokeDashoffset={metric.strokeDasharray ? undefined : (length ?? 99_999)}
      strokeLinecap="butt"
      strokeLinejoin="miter"
      strokeOpacity="0.8"
      strokeWidth="1.5"
      style={
        ready
          ? ({ "--metric-path-length": length } as CSSProperties)
          : undefined
      }
    />
  );
};

const MetricChartComponent = <T extends MetricPoint>({
  emptyLabel,
  formatValue,
  from,
  minimumMaximum,
  points: basePoints,
  series,
  title,
  to,
}: MetricChartProps<T>) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const [hover, setHover] = useState<HoverState>();

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }
    const observer = new ResizeObserver(([entry]) => {
      if (entry) {
        setWidth(Math.floor(entry.contentRect.width));
      }
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, []);

  const duration = Math.max(0, to - from);
  const plotHeight = chartHeight - padding.top - padding.bottom;
  const points = useMemo(() => {
    const byTime = new Map(
      basePoints.map((point) => [point.observedAt, point] as const)
    );
    for (const metric of series) {
      for (const point of metric.points ?? []) {
        if (!byTime.has(point.observedAt)) {
          byTime.set(point.observedAt, point);
        }
      }
    }
    return [...byTime.values()].toSorted(
      (first, second) => first.observedAt - second.observedAt
    );
  }, [basePoints, series]);
  const chartSeries = useMemo<MetricSeries<T>[]>(
    () =>
      series.map((metric) => {
        if (!metric.points) {
          return metric;
        }
        const byTime = new Map(
          metric.points.map((point) => [point.observedAt, point] as const)
        );
        return {
          ...metric,
          points: undefined,
          value: (timelinePoint) => {
            const point = byTime.get(timelinePoint.observedAt);
            return point ? metric.value(point) : undefined;
          },
        };
      }),
    [series]
  );
  const { hasValues, maximum, yTicks } = useMemo(() => {
    let highest = 0;
    let nextHasValues = false;
    for (const point of points) {
      for (const metric of chartSeries) {
        const value = metric.value(point);
        if (value !== undefined && Number.isFinite(value)) {
          highest = Math.max(highest, value);
          nextHasValues = true;
        }
      }
    }
    const nextMaximum = niceMaximum(Math.max(highest, minimumMaximum));
    const tickCount = nextMaximum <= 5 ? nextMaximum : 4;
    return {
      hasValues: nextHasValues,
      maximum: nextMaximum,
      yTicks: Array.from(
        { length: tickCount + 1 },
        (_, index) => (nextMaximum / tickCount) * index
      ),
    };
  }, [chartSeries, minimumMaximum, points]);
  const plotLeft = Math.max(
    minimumLeftPadding,
    Math.ceil(
      Math.max(...yTicks.map((tick) => formatValue(tick).length)) *
        yLabelCharacterWidth +
        12
    )
  );
  const plotWidth = Math.max(0, width - plotLeft - padding.right);
  const geometry = useMemo(
    () =>
      chartSeries
        .map((metric) =>
          geometryForSeries({
            maximum,
            metric,
            plotHeight,
            plotLeft,
            plotWidth,
            points,
          })
        )
        .toReversed(),
    [chartSeries, maximum, plotHeight, plotLeft, plotWidth, points]
  );
  const pointX = useCallback(
    (pointIndex: number) => {
      const xStep =
        points.length > 1 ? plotWidth / (points.length - 1) : plotWidth;
      return plotLeft + pointIndex * xStep;
    },
    [plotLeft, plotWidth, points.length]
  );

  const updateHover = useCallback(
    (pointIndex: number, pointerX: number, pointerY: number) => {
      setHover((current) => {
        if (
          current?.pointIndex === pointIndex &&
          Math.abs(current.pointerX - pointerX) < 1 &&
          Math.abs(current.pointerY - pointerY) < 1
        ) {
          return current;
        }
        return { pointIndex, pointerX, pointerY };
      });
    },
    []
  );

  const handlePointerMove = useCallback(
    (event: ReactPointerEvent<HTMLInputElement>) => {
      if (points.length === 0 || plotWidth <= 0) {
        return;
      }
      const bounds = containerRef.current?.getBoundingClientRect();
      if (!bounds) {
        return;
      }
      const pointerX = Math.max(
        plotLeft,
        Math.min(width - padding.right, event.clientX - bounds.left)
      );
      const pointIndex = Math.round(
        ((pointerX - plotLeft) / plotWidth) * Math.max(0, points.length - 1)
      );
      updateHover(
        pointIndex,
        event.clientX - bounds.left,
        event.clientY - bounds.top
      );
    },
    [plotLeft, plotWidth, points, updateHover, width]
  );

  const handleFocus = useCallback(() => {
    if (points.length === 0) {
      return;
    }
    const pointIndex = points.length - 1;
    updateHover(pointIndex, pointX(pointIndex), padding.top + plotHeight / 2);
  }, [plotHeight, pointX, points.length, updateHover]);

  const handleRangeChange = useCallback(
    (pointIndex: number) => {
      if (!points[pointIndex]) {
        return;
      }
      updateHover(pointIndex, pointX(pointIndex), padding.top + plotHeight / 2);
    },
    [plotHeight, pointX, points, updateHover]
  );

  const hoveredPoint = hover ? points[hover.pointIndex] : undefined;
  const sampleStep = useMemo(() => {
    let smallestStep = Number.POSITIVE_INFINITY;
    for (let index = 1; index < points.length; index += 1) {
      const point = points[index];
      const previousPoint = points[index - 1];
      if (point && previousPoint) {
        const step = point.observedAt - previousPoint.observedAt;
        if (step > 0) {
          smallestStep = Math.min(smallestStep, step);
        }
      }
    }
    return Number.isFinite(smallestStep) ? smallestStep : duration;
  }, [duration, points]);
  const crosshairX = hover ? pointX(hover.pointIndex) : undefined;
  const labelEvery = Math.max(1, Math.ceil((points.length - 1) / 4));
  const tooltipHeight = 34 + series.length * 19;
  const tooltipPosition = hover
    ? {
        left: Math.max(
          4,
          Math.min(hover.pointerX + 14, width - tooltipWidth - 4)
        ),
        top: Math.max(
          4,
          Math.min(
            hover.pointerY - tooltipHeight / 2,
            chartHeight - tooltipHeight - 4
          )
        ),
      }
    : undefined;

  return (
    <section className="min-w-0 overflow-hidden">
      <header className="flex min-h-12 flex-wrap items-center gap-x-4 gap-y-1 border-b border-border px-4 py-2.5">
        <h3 className="mr-auto text-xs font-medium tracking-[0.12em] uppercase">
          {title}
        </h3>
        {chartSeries.map((metric) => {
          const value = latestValue(points, metric);
          return (
            <span
              className="flex items-center gap-1.5 text-[10px] text-muted-foreground"
              key={metric.label}
            >
              <svg aria-hidden="true" className="h-1 w-3" viewBox="0 0 12 4">
                <line
                  stroke={metric.color}
                  strokeDasharray={metric.strokeDasharray}
                  strokeOpacity="0.8"
                  strokeWidth="2"
                  x1="0"
                  x2="12"
                  y1="2"
                  y2="2"
                />
              </svg>
              {metric.label}
              <span className="text-foreground tabular-nums">
                {value === undefined ? "—" : formatValue(value)}
              </span>
            </span>
          );
        })}
      </header>
      <div className="relative h-60 min-w-0 select-none" ref={containerRef}>
        {width > 0 ? (
          <svg
            className="block overflow-visible"
            height={chartHeight}
            width={width}
          >
            <title>{`${title} history`}</title>
            {yTicks.map((tick) => {
              const y =
                padding.top + plotHeight - (tick / maximum) * plotHeight;
              return (
                <g key={tick}>
                  <line
                    stroke="currentColor"
                    strokeDasharray={tick === 0 ? undefined : "2 3"}
                    strokeOpacity="0.08"
                    x1={plotLeft}
                    x2={width - padding.right}
                    y1={y}
                    y2={y}
                  />
                  <text
                    className="metric-chart-label fill-muted-foreground"
                    dominantBaseline="central"
                    textAnchor="end"
                    x={plotLeft - 7}
                    y={y}
                  >
                    {formatValue(tick)}
                  </text>
                </g>
              );
            })}

            {geometry.map(({ areas, metric }) =>
              metric.strokeDasharray
                ? null
                : areas.map((area, segmentIndex) => (
                    <path
                      className="metric-chart-area"
                      d={area}
                      fill={metric.color}
                      fillOpacity={metric === chartSeries[0] ? "0.12" : "0.08"}
                      key={`${metric.label}-area-${segmentIndex}`}
                    />
                  ))
            )}

            {geometry.map(({ lines, metric }) =>
              lines.map((line, segmentIndex) => (
                <AnimatedMetricLine
                  d={line}
                  key={`${metric.label}-line-${segmentIndex}`}
                  metric={metric}
                />
              ))
            )}

            {crosshairX === undefined ? null : (
              <line
                pointerEvents="none"
                stroke="currentColor"
                strokeDasharray="2 2"
                strokeOpacity="0.15"
                x1={crosshairX}
                x2={crosshairX}
                y1={padding.top}
                y2={padding.top + plotHeight}
              />
            )}

            {hover
              ? geometry.map(({ coordinates, metric }) => {
                  const coordinate = coordinates[hover.pointIndex];
                  return coordinate ? (
                    <circle
                      cx={coordinate.x}
                      cy={coordinate.y}
                      fill={metric.color}
                      key={`${metric.label}-hover-dot`}
                      pointerEvents="none"
                      r="3"
                    />
                  ) : null;
                })
              : null}

            {duration > 0
              ? points.map((point, pointIndex) => {
                  if (
                    pointIndex % labelEvery !== 0 &&
                    pointIndex !== points.length - 1
                  ) {
                    return null;
                  }
                  return (
                    <text
                      className="metric-chart-label fill-muted-foreground"
                      key={point.observedAt}
                      textAnchor={timeLabelAnchor(
                        pointIndex,
                        points.length - 1
                      )}
                      x={pointX(pointIndex)}
                      y={padding.top + plotHeight + 17}
                    >
                      {timelineLabel(point.observedAt - sampleStep, duration)}
                    </text>
                  );
                })
              : null}
          </svg>
        ) : null}

        <input
          aria-label={`${title} chart sample`}
          className="absolute cursor-crosshair appearance-none opacity-0"
          disabled={points.length === 0}
          max={Math.max(0, points.length - 1)}
          min="0"
          onBlur={() => setHover(undefined)}
          onChange={(event) =>
            handleRangeChange(event.currentTarget.valueAsNumber)
          }
          onFocus={handleFocus}
          onPointerLeave={() => setHover(undefined)}
          onPointerMove={handlePointerMove}
          step="1"
          style={{
            height: plotHeight,
            left: plotLeft,
            top: padding.top,
            width: plotWidth,
          }}
          type="range"
          value={hover?.pointIndex ?? Math.max(0, points.length - 1)}
        />
        {hasValues ? null : (
          <p className="pointer-events-none absolute inset-0 grid place-items-center text-[9px] text-muted-foreground">
            {emptyLabel}
          </p>
        )}

        {hoveredPoint && tooltipPosition ? (
          <div
            className="pointer-events-none absolute z-10 w-52 max-w-[calc(100%-0.5rem)] border border-border bg-card p-2.5 shadow-lg"
            style={tooltipPosition}
          >
            <div className="mb-1.5 text-[10px] text-muted-foreground">
              {timelineLabel(hoveredPoint.observedAt - sampleStep, duration)}
              {" — "}
              {timelineLabel(hoveredPoint.observedAt, duration)}
            </div>
            <div className="grid gap-1">
              {chartSeries.map((metric) => {
                const value = metric.value(hoveredPoint);
                return (
                  <div
                    className="flex items-center justify-between gap-6 text-[10px]"
                    key={metric.label}
                  >
                    <span className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
                      <span
                        className="inline-block h-0.5 w-2 shrink-0"
                        style={{ backgroundColor: metric.color }}
                      />
                      <span className="truncate">{metric.label}</span>
                    </span>
                    <span className="shrink-0 font-medium text-foreground tabular-nums">
                      {value === undefined ? "—" : formatValue(value)}
                    </span>
                  </div>
                );
              })}
            </div>
          </div>
        ) : null}
      </div>
    </section>
  );
};

export const MetricChart = memo(MetricChartComponent) as <
  T extends MetricPoint,
>(
  props: MetricChartProps<T>
) => ReactElement;
